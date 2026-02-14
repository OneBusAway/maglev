package realtime

import (
	"context"
	"math"
	"sync"
	"time"

	go_gtfs "github.com/OneBusAway/go-gtfs"
	"maglev.onebusaway.org/gtfsdb"
	"maglev.onebusaway.org/internal/gtfs"
)

type Service struct {
	gtfsManager *gtfs.Manager
	config      Config
	caches      *caches
}

type Config struct {
	StaleThreshold time.Duration
}

func DefaultConfig() Config {
	return Config{StaleThreshold: 15 * time.Minute}
}

func NewService(gm *gtfs.Manager, cfg Config) *Service {
	return &Service{
		gtfsManager: gm,
		config:      cfg,
		caches:      newCaches(),
	}
}

type VehiclePosition struct {
	VehicleID             string
	TripID                string
	ActiveTripID          string
	Latitude              float64
	Longitude             float64
	Distance              float64
	Timestamp             time.Time
	ScheduleDeviation     int
	CurrentStopID         string
	NextStopID            string
	CurrentStopTimeOffset int
	NextStopTimeOffset    int
	IsStale               bool
	IsPredicted           bool
}

func (s *Service) GetVehiclePosition(ctx context.Context, vehicleID string) (VehiclePosition, error) {
	vehicle, err := s.gtfsManager.GetVehicleByID(vehicleID)
	if err != nil {
		return VehiclePosition{}, err
	}

	pos := VehiclePosition{
		VehicleID: vehicle.ID.ID,
		Timestamp: time.Now(),
	}

	if vehicle.Trip != nil {
		pos.TripID = vehicle.Trip.ID.ID
	}

	if s.isStale(vehicle.Timestamp) {
		pos.IsStale = true
		pos.IsPredicted = false
		return pos, nil
	}

	pos = s.calculateScheduledPosition(ctx, vehicle, pos)
	pos.IsPredicted = true

	return pos, nil
}

func (s *Service) calculateScheduledPosition(ctx context.Context, vehicle *go_gtfs.Vehicle, pos VehiclePosition) VehiclePosition {
	if vehicle.Trip == nil {
		return pos
	}

	tripID := vehicle.Trip.ID.ID

	trip, err := s.gtfsManager.GtfsDB.Queries.GetTrip(ctx, tripID)
	if err != nil {
		return pos
	}

	now := time.Now()
	currentSeconds := int64(now.Hour()*3600 + now.Minute()*60 + now.Second())

	pos.ScheduleDeviation = s.GetTripDeviation(ctx, tripID)

	if pos.ScheduleDeviation == 0 && vehicle.Position != nil && vehicle.Position.Latitude != nil && vehicle.Position.Longitude != nil {
		recalculatedDeviation := s.calculateDeviationFromVehiclePosition(ctx, trip, vehicle, currentSeconds)
		if recalculatedDeviation != 0 {
			pos.ScheduleDeviation = recalculatedDeviation
		}
	}

	// Java formula: effectiveTime = currentTime - deviation
	// If vehicle is 573s late, effective time is 573s ago
	effectiveTime := currentSeconds - int64(pos.ScheduleDeviation)

	// If trip is in a block, calculate position across entire block
	if trip.BlockID.Valid {
		pos = s.calculateBlockPosition(ctx, trip, effectiveTime, pos)
	} else {
		// Single trip - calculate position within trip
		pos = s.calculateTripPosition(ctx, tripID, effectiveTime, pos)
	}

	return pos
}

func (s *Service) calculateDeviationFromVehiclePosition(ctx context.Context, trip gtfsdb.Trip, vehicle *go_gtfs.Vehicle, currentSeconds int64) int {
	if vehicle.Position == nil || vehicle.Position.Latitude == nil || vehicle.Position.Longitude == nil {
		return 0
	}

	lat := *vehicle.Position.Latitude
	lon := *vehicle.Position.Longitude

	stopTimes, err := s.gtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, trip.ID)
	if err != nil || len(stopTimes) == 0 {
		return 0
	}

	shapeRows, _ := s.gtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, trip.ID)
	totalDistance := s.calculateTripDistance(shapeRows)

	vehicleDistance := s.findDistanceAlongTripForLocation(ctx, stopTimes, float64(lat), float64(lon), totalDistance)
	if vehicleDistance < 0 {
		return 0
	}

	scheduledTimeAtPosition := s.getScheduledTimeAtDistance(vehicleDistance, stopTimes, totalDistance)
	if scheduledTimeAtPosition < 0 {
		return 0
	}

	deviation := currentSeconds - scheduledTimeAtPosition

	return int(deviation)
}

func (s *Service) findDistanceAlongTripForLocation(ctx context.Context, stopTimes []gtfsdb.StopTime, lat, lon, totalDistance float64) float64 {
	if len(stopTimes) == 0 || totalDistance <= 0 {
		return -1
	}

	closestStopDistance := 0.0

	stopIDs := make([]string, len(stopTimes))
	for i, st := range stopTimes {
		stopIDs[i] = st.StopID
	}

	for i := range stopTimes {
		stopDistance := float64(i) / float64(len(stopTimes)-1) * totalDistance
		if i == 0 {
			closestStopDistance = stopDistance
		}
	}

	return closestStopDistance
}

func (s *Service) getScheduledTimeAtDistance(distance float64, stopTimes []gtfsdb.StopTime, totalDistance float64) int64 {
	if len(stopTimes) == 0 || totalDistance <= 0 {
		return -1
	}

	for i := 0; i < len(stopTimes)-1; i++ {
		fromDist := float64(i) / float64(len(stopTimes)-1) * totalDistance
		toDist := float64(i+1) / float64(len(stopTimes)-1) * totalDistance

		if distance >= fromDist && distance <= toDist {
			fromTime := stopTimes[i].ArrivalTime / 1e9
			if fromTime == 0 {
				fromTime = stopTimes[i].DepartureTime / 1e9
			}

			toTime := stopTimes[i+1].ArrivalTime / 1e9
			if toTime == 0 {
				toTime = stopTimes[i+1].DepartureTime / 1e9
			}

			if toDist == fromDist {
				return fromTime
			}

			ratio := (distance - fromDist) / (toDist - fromDist)
			return int64(float64(fromTime) + ratio*float64(toTime-fromTime))
		}
	}

	lastTime := stopTimes[len(stopTimes)-1].ArrivalTime / 1e9
	if lastTime == 0 {
		lastTime = stopTimes[len(stopTimes)-1].DepartureTime / 1e9
	}
	return lastTime
}

func (s *Service) calculateBlockPosition(ctx context.Context, trip gtfsdb.Trip, effectiveTime int64, pos VehiclePosition) VehiclePosition {
	blockTrips, err := s.gtfsManager.GtfsDB.Queries.GetTripsByBlockIDOrdered(ctx, gtfsdb.GetTripsByBlockIDOrderedParams{
		BlockID:    trip.BlockID,
		ServiceIds: []string{trip.ServiceID},
	})
	if err != nil {
		return pos
	}

	var allStopTimes []BlockStopTime
	cumulativeDistance := 0.0

	for _, blockTrip := range blockTrips {
		stopTimes, _ := s.gtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, blockTrip.ID)
		shapeRows, _ := s.gtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, blockTrip.ID)
		tripDistance := s.calculateTripDistance(shapeRows)

		for i, st := range stopTimes {
			arrival := st.ArrivalTime / 1e9
			if arrival == 0 {
				arrival = st.DepartureTime / 1e9
			}

			stopDist := cumulativeDistance
			if len(stopTimes) > 1 {
				stopDist += float64(i) / float64(len(stopTimes)-1) * tripDistance
			}

			allStopTimes = append(allStopTimes, BlockStopTime{
				TripID:       blockTrip.ID,
				StopID:       st.StopID,
				ArrivalTime:  arrival,
				Distance:     stopDist,
				StopSequence: int(st.StopSequence),
			})
		}

		cumulativeDistance += tripDistance
	}

	if len(allStopTimes) == 0 {
		return pos
	}

	idx := s.findStopTimeIndex(allStopTimes, effectiveTime)

	if idx < 0 {
		pos.ActiveTripID = allStopTimes[0].TripID
		pos.CurrentStopID = allStopTimes[0].StopID
		pos.Distance = allStopTimes[0].Distance
		pos.CurrentStopTimeOffset = int(allStopTimes[0].ArrivalTime - effectiveTime)
		if len(allStopTimes) > 1 {
			pos.NextStopID = allStopTimes[1].StopID
			pos.NextStopTimeOffset = int(allStopTimes[1].ArrivalTime - effectiveTime)
		}
	} else if idx >= len(allStopTimes)-1 {
		lastIdx := len(allStopTimes) - 1
		pos.ActiveTripID = allStopTimes[lastIdx].TripID
		pos.CurrentStopID = allStopTimes[lastIdx].StopID
		pos.Distance = allStopTimes[lastIdx].Distance
		pos.CurrentStopTimeOffset = int(allStopTimes[lastIdx].ArrivalTime - effectiveTime)
	} else {
		fromStop := allStopTimes[idx]
		toStop := allStopTimes[idx+1]

		fromTime := fromStop.ArrivalTime
		toTime := toStop.ArrivalTime

		var ratio float64
		if toTime > fromTime {
			ratio = float64(effectiveTime-fromTime) / float64(toTime-fromTime)
		}

		pos.Distance = fromStop.Distance + ratio*(toStop.Distance-fromStop.Distance)
		pos.ActiveTripID = fromStop.TripID
		pos.CurrentStopID = fromStop.StopID
		pos.NextStopID = toStop.StopID
		pos.CurrentStopTimeOffset = int(fromTime - effectiveTime)
		pos.NextStopTimeOffset = int(toTime - effectiveTime)
	}

	return pos
}

func (s *Service) findStopTimeIndex(allStopTimes []BlockStopTime, effectiveTime int64) int {
	low, high := 0, len(allStopTimes)-1

	for low <= high {
		mid := (low + high) / 2
		if allStopTimes[mid].ArrivalTime <= effectiveTime {
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	return high
}

func (s *Service) calculateTripPosition(ctx context.Context, tripID string, effectiveTime int64, pos VehiclePosition) VehiclePosition {
	stopTimes, _ := s.gtfsManager.GtfsDB.Queries.GetStopTimesForTrip(ctx, tripID)
	if len(stopTimes) == 0 {
		return pos
	}

	shapeRows, _ := s.gtfsManager.GtfsDB.Queries.GetShapePointsByTripID(ctx, tripID)
	totalDistance := s.calculateTripDistance(shapeRows)

	type stopInfo struct {
		stopID      string
		arrivalTime int64
		distance    float64
	}
	stopInfos := make([]stopInfo, len(stopTimes))
	for i, st := range stopTimes {
		arrival := st.ArrivalTime / 1e9
		if arrival == 0 {
			arrival = st.DepartureTime / 1e9
		}
		dist := 0.0
		if len(stopTimes) > 1 {
			dist = float64(i) / float64(len(stopTimes)-1) * totalDistance
		}
		stopInfos[i] = stopInfo{
			stopID:      st.StopID,
			arrivalTime: arrival,
			distance:    dist,
		}
	}

	idx := -1
	for i := 0; i < len(stopInfos)-1; i++ {
		if effectiveTime >= stopInfos[i].arrivalTime && effectiveTime < stopInfos[i+1].arrivalTime {
			idx = i
			break
		}
	}

	if idx < 0 {
		if effectiveTime < stopInfos[0].arrivalTime {
			pos.CurrentStopID = stopInfos[0].stopID
			pos.Distance = stopInfos[0].distance
			pos.CurrentStopTimeOffset = int(stopInfos[0].arrivalTime - effectiveTime)
			if len(stopInfos) > 1 {
				pos.NextStopID = stopInfos[1].stopID
				pos.NextStopTimeOffset = int(stopInfos[1].arrivalTime - effectiveTime)
			}
		} else {
			lastIdx := len(stopInfos) - 1
			pos.CurrentStopID = stopInfos[lastIdx].stopID
			pos.Distance = stopInfos[lastIdx].distance
			pos.CurrentStopTimeOffset = int(stopInfos[lastIdx].arrivalTime - effectiveTime)
		}
	} else {
		fromStop := stopInfos[idx]
		toStop := stopInfos[idx+1]

		fromTime := fromStop.arrivalTime
		toTime := toStop.arrivalTime

		var ratio float64
		if toTime > fromTime {
			ratio = float64(effectiveTime-fromTime) / float64(toTime-fromTime)
		}

		pos.Distance = fromStop.distance + ratio*(toStop.distance-fromStop.distance)
		pos.CurrentStopID = fromStop.stopID
		pos.NextStopID = toStop.stopID
		pos.CurrentStopTimeOffset = int(fromTime - effectiveTime)
		pos.NextStopTimeOffset = int(toTime - effectiveTime)
	}

	pos.ActiveTripID = tripID
	return pos
}

func (s *Service) GetTripDeviation(ctx context.Context, tripID string) int {
	if cached := s.caches.deviation.get(tripID); cached != nil {
		if dev, ok := cached.(int); ok {
			return dev
		}
	}

	updates := s.gtfsManager.GetTripUpdatesForTrip(tripID)
	if len(updates) == 0 {
		return 0
	}

	tu := updates[0]
	deviation := 0

	if tu.Delay != nil {
		deviation = int(tu.Delay.Seconds())
	} else {
		for _, stu := range tu.StopTimeUpdates {
			if stu.Arrival != nil && stu.Arrival.Delay != nil {
				deviation = int(stu.Arrival.Delay.Seconds())
				break
			}
		}
	}

	s.caches.deviation.set(tripID, deviation)
	return deviation
}

func (s *Service) isStale(timestamp *time.Time) bool {
	if timestamp == nil {
		return true
	}
	return time.Since(*timestamp) > s.config.StaleThreshold
}

func (s *Service) calculateTripDistance(shapeRows []gtfsdb.Shape) float64 {
	distance := 0.0
	for i := 1; i < len(shapeRows); i++ {
		distance += haversine(shapeRows[i-1].Lat, shapeRows[i-1].Lon, shapeRows[i].Lat, shapeRows[i].Lon)
	}
	return distance
}

type BlockStopTime struct {
	TripID       string
	StopID       string
	ArrivalTime  int64
	Distance     float64
	StopSequence int
}

func haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const R = 6371000
	phi1 := lat1 * math.Pi / 180
	phi2 := lat2 * math.Pi / 180
	deltaPhi := (lat2 - lat1) * math.Pi / 180
	deltaLambda := (lon2 - lon1) * math.Pi / 180

	a := math.Sin(deltaPhi/2)*math.Sin(deltaPhi/2) +
		math.Cos(phi1)*math.Cos(phi2)*
			math.Sin(deltaLambda/2)*math.Sin(deltaLambda/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return R * c
}

type caches struct {
	vehicle   *syncMap
	deviation *syncMap
}

func newCaches() *caches {
	return &caches{
		vehicle:   &syncMap{m: make(map[string]interface{})},
		deviation: &syncMap{m: make(map[string]interface{})},
	}
}

type syncMap struct {
	sync.RWMutex
	m map[string]interface{}
}

func (sm *syncMap) get(key string) interface{} {
	sm.RLock()
	defer sm.RUnlock()
	return sm.m[key]
}

func (sm *syncMap) set(key string, value interface{}) {
	sm.Lock()
	defer sm.Unlock()
	sm.m[key] = value
}
