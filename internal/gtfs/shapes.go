package gtfs

// IMPORTANT: Caller must hold manager.RLock() before calling this method.
func (manager *Manager) GetRegionBounds() (lat, lon, latSpan, lonSpan float64) {
	var minLat, maxLat, minLon, maxLon float64
	first := true
	for _, shape := range manager.gtfsData.Shapes {
		for _, point := range shape.Points {
			if first {
				minLat = point.Latitude
				maxLat = point.Latitude
				minLon = point.Longitude
				maxLon = point.Longitude
				first = false
				continue
			}

			if point.Latitude < minLat {
				minLat = point.Latitude
			}
			if point.Latitude > maxLat {
				maxLat = point.Latitude
			}
			if point.Longitude < minLon {
				minLon = point.Longitude
			}
			if point.Longitude > maxLon {
				maxLon = point.Longitude
			}
		}
	}

	lat = (minLat + maxLat) / 2
	lon = (minLon + maxLon) / 2
	latSpan = maxLat - minLat
	lonSpan = maxLon - minLon

	return lat, lon, latSpan, lonSpan
}
