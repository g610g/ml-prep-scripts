import geopandas as gpd

zone = gpd.read_file("./Bulk Density_north_cotabato_zonal.gpkg")
print(zone.head())