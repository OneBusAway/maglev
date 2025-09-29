package gtfs

import (
	"github.com/OneBusAway/go-gtfs"
)

func (m *Manager) MockAddAgency(id, name string) {
	for _, a := range m.gtfsData.Agencies {
		if a.Id == id {
			return
		}
	}
	m.gtfsData.Agencies = append(m.gtfsData.Agencies, gtfs.Agency{
		Id:   id,
		Name: name,
	})
}

func (m *Manager) MockAddRoute(id, agencyID, name string) {
	for _, r := range m.gtfsData.Routes {
		if r.Id == id {
			return
		}
	}
	m.gtfsData.Routes = append(m.gtfsData.Routes, gtfs.Route{
		Id:        id,
		Agency:    &gtfs.Agency{Id: agencyID},
		ShortName: name,
	})
}
func (m *Manager) MockAddVehicle(vehicleID, tripID, routeID string) {
	for _, v := range m.realTimeVehicles {
		if v.ID.ID == vehicleID {
			return
		}
	}
	m.realTimeVehicles = append(m.realTimeVehicles, gtfs.Vehicle{
		ID: &gtfs.VehicleID{ID: vehicleID},
		Trip: &gtfs.Trip{
			ID: gtfs.TripID{
				ID:      tripID,
				RouteID: routeID,
			},
		},
	})

	m.realTimeVehicleLookupByVehicle[vehicleID] = len(m.realTimeVehicles) - 1
}

func (m *Manager) MockAddTrip(tripID, agencyID, routeID string) {
	for _, t := range m.gtfsData.Trips {
		if t.ID == tripID {
			return
		}
	}
	m.gtfsData.Trips = append(m.gtfsData.Trips, gtfs.ScheduledTrip{
		ID:    tripID,
		Route: &gtfs.Route{Id: routeID},
	})
}
