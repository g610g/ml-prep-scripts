package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	// Zone is a raster grid (not a shapefile) — already in EPSG:3123
	zoneRaster        = "NorthCotabato_30m_grid.tif"
	rasterFolder      = "rasters/TIF Files"
	reprojectedFolder = "reprojected/"
	targetCRS         = "EPSG:3123"
	noDataValue       = "-9999"
)

type ZonalResult struct {
	RasterName string
	CSVPath    string
	Error      error
	Duration   time.Duration
}

// ── Step 1: Reproject a precipitation/rainfall raster to EPSG:3123 ──────────
// The zone raster is already in EPSG:3123 so we only reproject the input rasters.
func reprojectRaster(src, dstFolder, targetCRS string) (string, error) {
	base := filepath.Base(src)
	dst := filepath.Join(dstFolder, base)

	if _, err := os.Stat(dst); err == nil {
		fmt.Printf("[Reproject] Skipping (exists): %s\n", base)
		return dst, nil
	}

	cmd := exec.Command("gdalwarp",
		"-t_srs", targetCRS,
		"-r", "near",
		"-dstnodata", noDataValue,
		"-overwrite",
		src, dst,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Printf("[Reproject] %s -> %s\n", base, dstFolder)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gdalwarp failed on %s: %w", base, err)
	}
	return dst, nil
}

// ── Step 2: Compute zonal stats ──────────────────────────────────────────────
// Zone = NorthCotabato_10m_grid.tif (each pixel is one 10x10m cell, value=1)
// Value raster = reprojected precipitation/rainfall .tif
// rasterstats reads pixels from the value raster that align with zone pixels.
var zonalStatScript = `
import sys
import json
import numpy as np
import rasterio
from rasterio.warp import reproject, Resampling
import geopandas as gpd
from shapely.geometry import box

zone_raster     = sys.argv[1]
value_raster    = sys.argv[2]
output_gpkg     = sys.argv[3]
nodata_val      = float(sys.argv[4])
resample_method = sys.argv[5]
resampling      = Resampling.bilinear if resample_method == "bilinear" else Resampling.nearest

CHUNK_SIZE = 500_000

print(f"[Python] Zone raster    : {zone_raster}",    file=sys.stderr)
print(f"[Python] Value raster   : {value_raster}",   file=sys.stderr)
print(f"[Python] Output GPKG    : {output_gpkg}",    file=sys.stderr)
print(f"[Python] Resample method: {resample_method}", file=sys.stderr)

# ── Step 1: Read zone raster ──────────────────────────────────────────────────
with rasterio.open(zone_raster) as z:
    zone_data      = z.read(1)
    zone_transform = z.transform
    zone_crs       = z.crs
    zone_shape     = z.shape

print(f"[Python] Zone grid: {zone_shape[1]} cols x {zone_shape[0]} rows", file=sys.stderr)

# ── Step 2: Resample value raster to match 10x10m zone grid ──────────────────
with rasterio.open(value_raster) as v:
    src_res      = v.res
    value_nodata = v.nodata if v.nodata is not None else nodata_val

    print(f"[Python] Value raster resolution: {src_res[0]}m x {src_res[1]}m", file=sys.stderr)

    value_data = np.empty(zone_shape, dtype=np.float32)
    reproject(
        source        = rasterio.band(v, 1),
        destination   = value_data,
        src_transform = v.transform,
        src_crs       = v.crs,
        dst_transform = zone_transform,
        dst_crs       = zone_crs,
        dst_shape     = zone_shape,
        resampling    = resampling,
    )

print(f"[Python] Resampling complete", file=sys.stderr)

# ── Step 3: Get valid cell indices (inside North Cotabato only) ───────────────
rows_idx, cols_idx = np.where(zone_data == 1)
total_cells        = len(rows_idx)
print(f"[Python] Valid cells inside boundary: {total_cells:,}", file=sys.stderr)

# ── Step 4: Extract values and mask nodata ────────────────────────────────────
values      = value_data[rows_idx, cols_idx].astype(float)
nodata_mask = (values == value_nodata) | (values == nodata_val) | np.isnan(values)
values[nodata_mask] = np.nan

print(f"[Python] Cells with valid values: {int(np.sum(~nodata_mask)):,}", file=sys.stderr)

# ── Step 5: Prepare spatial metadata ─────────────────────────────────────────
half        = abs(zone_transform.a) / 2.0   # 5.0m — half of 10m cell
raster_name = value_raster.split("/")[-1].replace(".tif", "")
layer_name  = raster_name.replace(" ", "_").lower()

# ── Step 6: Write to GeoPackage in chunks using geopandas ────────────────────
# Each chunk builds a GeoDataFrame with only 2 columns: mean_value + geometry
# First chunk writes fresh (mode="w"), remaining chunks append (mode="a")
total_written = 0
first_chunk   = True
n_chunks      = (total_cells + CHUNK_SIZE - 1) // CHUNK_SIZE

print(f"[Python] Writing {total_cells:,} cells across {n_chunks} chunks...", file=sys.stderr)

for chunk_num, start in enumerate(range(0, total_cells, CHUNK_SIZE), 1):
    end = min(start + CHUNK_SIZE, total_cells)

    c_rows = rows_idx[start:end]
    c_cols = cols_idx[start:end]
    c_vals = values[start:end]

    # Skip chunks where everything is nodata
    if np.all(np.isnan(c_vals)):
        print(f"[Python] Chunk {chunk_num}/{n_chunks} — all nodata, skipping", file=sys.stderr)
        continue

    # Compute cell centroids
    c_east  = zone_transform.c + (c_cols + 0.5) * zone_transform.a
    c_north = zone_transform.f + (c_rows + 0.5) * zone_transform.e

    # Build 10x10m square geometry for each cell
    c_geoms = [
        box(
            c_east[i]  - half,   # west  edge
            c_north[i] - half,   # south edge
            c_east[i]  + half,   # east  edge
            c_north[i] + half,   # north edge
        )
        for i in range(len(c_rows))
    ]

    # ── Only 2 columns: mean_value + geometry ────────────────────────────────
    chunk_gdf = gpd.GeoDataFrame(
        {"mean_value": c_vals},
        geometry = c_geoms,
        crs      = zone_crs,
    )

    # Drop nodata rows
    chunk_gdf = chunk_gdf.dropna(subset=["mean_value"])
    if len(chunk_gdf) == 0:
        continue

    # First chunk: create fresh GPKG file
    # Remaining chunks: append to same layer
    mode = "w" if first_chunk else "a"
    chunk_gdf.to_file(
        output_gpkg,
        layer  = layer_name,
        driver = "GPKG",
        mode   = mode,
    )

    first_chunk    = False
    total_written += len(chunk_gdf)
    print(f"[Python] Chunk {chunk_num}/{n_chunks} — {total_written:,} / {total_cells:,} cells written", file=sys.stderr)

print(f"[Python] Done — {total_written:,} cells -> {output_gpkg} (layer: {layer_name})", file=sys.stderr)
print(json.dumps({
    "status"             : "ok",
    "rows"               : total_written,
    "gpkg"               : output_gpkg,
    "layer"              : layer_name,
    "src_res_m"          : float(src_res[0]),
    "cells_per_src_pixel": round((src_res[0] / 10.0) ** 2),
}))
`

func isCategorical(name string) bool {
	categorical := []string{
		"Land Cover", "Soil Type", "Geomorphology",
		"Geomorphology", "Land Cover",
	}
	for _, c := range categorical {
		if strings.Contains(name, c) {
			return true
		}
	}
	return false
}

func computeZonalStats(zoneRaster, valueRaster, baseName, outputGPKG, resampleMethod string) (int, string, error) {
	tmpScript, err := os.CreateTemp("", "zonalstat_*.py")
	if err != nil {
		return 0, "", fmt.Errorf("failed to create temp script: %w", err)
	}
	defer os.Remove(tmpScript.Name())
	tmpScript.WriteString(zonalStatScript)
	tmpScript.Close()

	cmd := exec.Command("python3",
		tmpScript.Name(),
		zoneRaster,
		valueRaster,
		outputGPKG,
		noDataValue,
		resampleMethod,
	)

	// Capture stderr separately so we get the FULL Python traceback
	var stderrBuf strings.Builder
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf) // print live AND capture

	out, err := cmd.Output()
	if err != nil {
		// Return the FULL stderr so we see the actual Python exception
		return 0, "", fmt.Errorf("python error:\n%s", stderrBuf.String())
	}

	var result map[string]interface{}
	if err := json.Unmarshal(out, &result); err != nil {
		return 0, "", fmt.Errorf("JSON parse error: %w\nPython output was: %s", err, string(out))
	}

	rows := int(result["rows"].(float64))
	layer := result["layer"].(string)
	return rows, layer, nil
}

// ── Step 3: Save results to CSV ──────────────────────────────────────────────
func saveCSV(records []map[string]interface{}, outputPath string) error {
	if len(records) == 0 {
		return fmt.Errorf("no records to save")
	}

	f, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	w := csv.NewWriter(f)
	defer w.Flush()

	// Stable column order: metadata first, then stats
	statKeys := []string{"mean", "min", "max", "std", "count", "sum"}
	var attrKeys []string
	for k := range records[0] {
		isStat := false
		for _, s := range statKeys {
			if k == s {
				isStat = true
				break
			}
		}
		if !isStat {
			attrKeys = append(attrKeys, k)
		}
	}
	headers := append(attrKeys, statKeys...)
	w.Write(headers)

	for _, rec := range records {
		row := make([]string, len(headers))
		for i, h := range headers {
			row[i] = fmt.Sprintf("%v", rec[h])
		}
		w.Write(row)
	}
	return nil
}

// ── Full pipeline for one raster ─────────────────────────────────────────────
func processRaster(
	rasterPath string,
	wg *sync.WaitGroup,
	results chan<- ZonalResult,
	sem chan struct{},
) {
	defer wg.Done()
	defer func() { <-sem }()

	start := time.Now()
	baseName := strings.TrimSuffix(filepath.Base(rasterPath), ".tif")

	outputGPKG := fmt.Sprintf("./30m/%s_north_cotabato_zonal.gpkg", baseName)
	csvPath := fmt.Sprintf("%s_north_cotabato_zonal.gpkg", baseName)

	// A: Reproject value raster to EPSG:3123
	reprojPath, err := reprojectRaster(rasterPath, reprojectedFolder, targetCRS)
	if err != nil {
		results <- ZonalResult{RasterName: baseName, Error: fmt.Errorf("reproject: %w", err)}
		return
	}

	// B: Per-cell sampling — one CSV row per 10x10m cell
	fmt.Printf("[ZonalStats] %-30s sampling per cell...\n", baseName)
	rows, _, err := computeZonalStats(zoneRaster, reprojPath, baseName, outputGPKG, csvPath)
	if err != nil {
		results <- ZonalResult{RasterName: baseName, Error: fmt.Errorf("zonal stats: %w", err)}
		return
	}

	fmt.Printf("[ZonalStats] %-30s → %d rows written\n", baseName, rows)
	results <- ZonalResult{
		RasterName: baseName,
		CSVPath:    csvPath,
		Duration:   time.Since(start),
	}
}

// ── Main ─────────────────────────────────────────────────────────────────────
func main() {
	totalStart := time.Now()

	// Verify zone raster exists
	if _, err := os.Stat(zoneRaster); err != nil {
		log.Fatalf("Zone raster not found: %s — run the grid creation step first", zoneRaster)
	}
	fmt.Printf("✅ Zone raster   : %s\n", zoneRaster)

	// Create reprojected output folder
	if err := os.MkdirAll(reprojectedFolder, 0755); err != nil {
		log.Fatalf("Cannot create reprojected folder: %v", err)
	}

	// Discover all input rasters
	entries, err := os.ReadDir(rasterFolder)
	if err != nil {
		log.Fatalf("Cannot read raster folder %s: %v", rasterFolder, err)
	}

	var rasters []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".tif") {
			rasters = append(rasters, filepath.Join(rasterFolder, entry.Name()))
		}
	}

	if len(rasters) == 0 {
		log.Fatalf("No .tif files found in %s", rasterFolder)
	}
	fmt.Printf("✅ Rasters found : %d\n", len(rasters))
	for _, r := range rasters {
		fmt.Printf("   • %s\n", filepath.Base(r))
	}
	fmt.Println()

	// Run all rasters in parallel, capped at NumCPU
	maxWorkers := runtime.NumCPU()
	if maxWorkers > len(rasters) {
		maxWorkers = len(rasters)
	}
	fmt.Printf("⚙  Workers       : %d (CPU cores)\n\n", maxWorkers)

	sem := make(chan struct{}, maxWorkers)
	resultsChan := make(chan ZonalResult, len(rasters))
	var wg sync.WaitGroup

	for _, raster := range rasters {
		wg.Add(1)
		sem <- struct{}{}
		go processRaster(raster, &wg, resultsChan, sem)
	}

	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	fmt.Println("══════════════════════════════════════════════════════")
	fmt.Println("  Results")
	fmt.Println("══════════════════════════════════════════════════════")

	success, failed := 0, 0
	for r := range resultsChan {
		if r.Error != nil {
			fmt.Printf("❌  %-30s ERROR: %v\n", r.RasterName, r.Error)
			failed++
		} else {
			fmt.Printf("✅  %-30s -> %-45s (%.2fs)\n", r.RasterName, r.CSVPath, r.Duration.Seconds())
			success++
		}
	}

	fmt.Println()
	fmt.Printf("Done — %d succeeded, %d failed | Total: %s\n",
		success, failed, time.Since(totalStart))
}
