import geopandas as gpd
from shapely.geometry import box
import numpy as np

# Load the polygon
boundary = gpd.read_file("boundary_utm.shp")

# Get the total bounds
minx, miny, maxx, maxy = boundary.total_bounds

cell_size = 10  # 10 meters
polygons = []

# Create the 10x10 m grid
for x in np.arange(minx, maxx, cell_size):
    for y in np.arange(miny, maxy, cell_size):
        cell = box(x, y, x + cell_size, y + cell_size)
        if boundary.geometry.unary_union.intersects(cell):
            polygons.append(cell)

# Save grid to shapefile
grid = gpd.GeoDataFrame({"geometry": polygons}, crs=boundary.crs)
grid.to_file("grid_full.shp")
