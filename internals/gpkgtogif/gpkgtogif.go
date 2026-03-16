package gpkgtogif

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	gridSize  = "30"       // grid resolution in meters
	gpkgDir   = "30m"      // folder containing the zonal GPKGs
	outputDir = "30m/tifs" // folder where visualize .tif files will be saved
)

func run(name string, args ...string) error {
	fmt.Printf("▶ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func gpkgToTif(gpkgPath string) error {
	base := strings.TrimSuffix(filepath.Base(gpkgPath), ".gpkg")

	// ── Output goes to outputDir folder ──────────────────────────────────────
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("cannot create output dir: %w", err)
	}
	outputTif := filepath.Join(outputDir, base+"_visualize.tif")

	fmt.Printf("\n══════════════════════════════════════════\n")
	fmt.Printf("  Converting : %s\n", filepath.Base(gpkgPath))
	fmt.Printf("  Output     : %s\n", outputTif)
	fmt.Printf("══════════════════════════════════════════\n")

	// ── Step 1: Get layer name from GPKG ─────────────────────────────────────
	cmd := exec.Command("ogrinfo", "-al", "-so", gpkgPath)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("ogrinfo failed: %w", err)
	}

	layerName := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "Layer name:") {
			layerName = strings.TrimSpace(strings.TrimPrefix(line, "Layer name:"))
			break
		}
	}
	if layerName == "" {
		return fmt.Errorf("could not find layer name in %s", gpkgPath)
	}
	fmt.Printf("  Layer      : %s\n\n", layerName)

	// ── Step 2: Fix missing extent before rasterizing ────────────────────────
	// Some GPKGs written by chunked geopandas have empty gpkg_contents extent
	// which causes "Cannot get layer extent" error in gdal_rasterize
	fmt.Println("  Fixing GPKG extent...")
	fixSQL := fmt.Sprintf(`
		UPDATE gpkg_contents
		SET
			min_x = (SELECT MIN(ST_MinX(geom)) FROM "%s"),
			min_y = (SELECT MIN(ST_MinY(geom)) FROM "%s"),
			max_x = (SELECT MAX(ST_MaxX(geom)) FROM "%s"),
			max_y = (SELECT MAX(ST_MaxY(geom)) FROM "%s")
		WHERE table_name = '%s'
		  AND (min_x IS NULL OR min_x = 0);
	`, layerName, layerName, layerName, layerName, layerName)

	fixCmd := exec.Command("ogrinfo", gpkgPath,
		"-dialect", "SQLite",
		"-sql", fixSQL,
	)
	fixCmd.Stdout = os.Stdout
	fixCmd.Stderr = os.Stderr
	if err := fixCmd.Run(); err != nil {
		// Non-fatal — gdal_rasterize may still work
		fmt.Printf("  ⚠️  Extent fix skipped (may already be set): %v\n", err)
	} else {
		fmt.Println("  ✅ Extent fixed")
	}

	// ── Step 3: Burn mean_value → GeoTIFF ────────────────────────────────────
	err = run("gdal_rasterize",
		"-l", layerName,
		"-a", "mean_value",
		"-tr", gridSize, gridSize,
		"-a_nodata", "-9999",
		"-ot", "Float32",
		"-of", "GTiff",
		"-co", "COMPRESS=LZW",
		"-co", "TILED=YES",
		"-co", "BIGTIFF=YES",
		gpkgPath,
		outputTif,
	)
	if err != nil {
		return fmt.Errorf("gdal_rasterize failed: %w", err)
	}

	// ── Step 4: Build overviews for fast QGIS rendering ──────────────────────
	fmt.Println("\nBuilding overviews (pyramids)...")
	err = run("gdaladdo",
		"-r", "average",
		"--config", "COMPRESS_OVERVIEW", "LZW",
		outputTif,
		"2", "4", "8", "16", "32", "64", "128",
	)
	if err != nil {
		return fmt.Errorf("gdaladdo failed: %w", err)
	}

	// ── Print output info ─────────────────────────────────────────────────────
	info, _ := os.Stat(outputTif)
	if info != nil {
		fmt.Printf("\n  ✅ Written: %s (%.1f MB)\n", outputTif, float64(info.Size())/1024/1024)
	}

	return nil
}

func Convert() {
	var gpkgFiles []string

	if len(os.Args) > 1 {
		// Files passed as CLI arguments — use as-is
		gpkgFiles = os.Args[1:]
	} else {
		// Auto-discover all *_zonal.gpkg files in gpkgDir
		entries, err := os.ReadDir(gpkgDir)
		if err != nil {
			log.Fatalf("Cannot read directory %s: %v", gpkgDir, err)
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), "_zonal.gpkg") {
				// ← BUG FIX: include full path with folder prefix
				gpkgFiles = append(gpkgFiles, filepath.Join(gpkgDir, e.Name()))
			}
		}
	}

	if len(gpkgFiles) == 0 {
		log.Fatalf("No *_zonal.gpkg files found in %s/", gpkgDir)
	}

	fmt.Printf("Found %d GPKG file(s) to convert\n\n", len(gpkgFiles))

	success, failed := 0, 0
	for _, gpkg := range gpkgFiles {
		if err := gpkgToTif(gpkg); err != nil {
			fmt.Printf("❌  %-45s %v\n", filepath.Base(gpkg), err)
			failed++
		} else {
			// ← BUG FIX: output tif is in outputDir, not current directory
			base := strings.TrimSuffix(filepath.Base(gpkg), ".gpkg")
			tif := filepath.Join(outputDir, base+"_visualize.tif")
			info, _ := os.Stat(tif)
			sizStr := ""
			if info != nil {
				sizStr = fmt.Sprintf("%.1f MB", float64(info.Size())/1024/1024)
			}
			fmt.Printf("✅  %-45s → %s (%s)\n", filepath.Base(gpkg), tif, sizStr)
			success++
		}
	}

	fmt.Printf("\nDone — %d succeeded, %d failed\n", success, failed)
	fmt.Printf("\nTIF files saved to: %s/\n", outputDir)
	fmt.Println("\nOpen in QGIS:")
	fmt.Println("  1. Drag any *_visualize.tif from", outputDir, "into QGIS")
	fmt.Println("  2. Properties → Symbology → Singleband pseudocolor")
	fmt.Println("  3. Choose color ramp → Classify → Apply")
}
