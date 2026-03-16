package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"zonal_parallel/internals/gpkgtogif"
	// "zonal_parallel/gpkgtogif"
)

func run(name string, args ...string) {
	fmt.Printf("▶ %s %s\n", name, strings.Join(args, " "))

	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		log.Fatalf("Command failed [%s]: %v", name, err)
	}
	fmt.Println("  ✓ Done")
}

// runCapture runs a command and returns its stdout as a string
func runCapture(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	cmd.Run()
	return strings.TrimSpace(out.String())
}

func main() {
	// inputSHP := "./rasters/North Cotabato brgy Boundary/NorthCotabato_MunicipalBoundary.shp"
	//
	// s := "30"
	//
	// tiffFile := fmt.Sprintf("NorthCotabato_%sm_grid.tif", s)
	// // ── Step 1: Reproject boundary to PRS92 UTM Zone 51N ────────────────
	// fmt.Println("Step 1: Reprojecting boundary to EPSG:3123...")
	// run("ogr2ogr",
	// 	"-f", "ESRI Shapefile",
	// 	"-t_srs", "EPSG:3123",
	// 	"-overwrite",
	// 	"boundary_utm.shp",
	// 	inputSHP,
	// )
	//
	// // Print extent so we can verify the boundary looks correct
	// fmt.Println("\n  Boundary info:")
	// info := runCapture("ogrinfo", "-al", "-so", "boundary_utm.shp")
	// fmt.Println(info)
	//
	// // ── Step 2: Create a 10x10m raster mask ─────────────────────────────
	// // This our grid. Each pixel = one sxs m cell.
	// // Pixels inside North Cotabato boundary = 1
	// // Pixels outside                        = 0 (nodata)
	// //
	// // -tr 10 10        → pixel size 10x10 meters
	// // -burn 1          → set all pixels inside boundary to value 1
	// // -init 0          → initialize raster to 0 first
	// // -a_nodata -9999  → mark outside pixels as nodata for zonal stats
	// fmt.Printf("Step 2: Creating %sx%sm raster grid (this is your cell grid)...", s, s)
	// run("gdal_rasterize",
	// 	"-burn", "1",
	// 	"-tr", s, s,
	// 	"-a_nodata", "-9999",
	// 	"-ot", "Int16",
	// 	"-of", "GTiff",
	// 	"-init", "-9999", // outside boundary = nodata
	// 	"-co", "COMPRESS=LZW", // compress to keep file size manageable
	// 	"-co", "TILED=YES", // tiled for faster spatial access
	// 	"boundary_utm.shp",
	// 	tiffFile,
	// )
	//
	// // Print raster info to verify cell count and resolution
	// fmt.Println("\n  Raster grid info:")
	// rasterInfo := runCapture("gdalinfo", "-mm", tiffFile)
	// fmt.Println(rasterInfo)
	//
	// // ── Step 3: Verify cell count ────────────────────────────────────────
	// // Use gdalinfo to confirm pixel dimensions match expected ~83M cells
	// fmt.Println("\nStep 3: Verifying grid...")
	// run("gdalinfo", tiffFile)
	//
	// // ── Cleanup ──────────────────────────────────────────────────────────
	// fmt.Println("\nCleaning up intermediate files...")
	// for _, f := range []string{
	// 	"boundary_utm.shp", "boundary_utm.dbf",
	// 	"boundary_utm.shx", "boundary_utm.prj", "boundary_utm.cpg",
	// } {
	// 	if err := os.Remove(f); err == nil {
	// 		fmt.Printf("  removed %s\n", f)
	// 	}
	// }
	//
	// fmt.Println()
	// fmt.Printf("✅ Done — %s created", tiffFile)
	// fmt.Println()
	// fmt.Println("   This raster IS your 10x10m cell grid:")
	// fmt.Println("   • Each pixel = one 10x10m cell")
	// fmt.Println("   • Value  1    = inside North Cotabato boundary")
	// fmt.Println("   • Value -9999 = outside boundary (nodata)")
	// fmt.Println()
	// fmt.Println("   Use this .tif directly with rasterstats in your zonal pipeline:")
	// fmt.Printf("   zonal_stats(zones, %s, nodata=-9999)", tiffFile)
	gpkgtogif.Convert()

}
