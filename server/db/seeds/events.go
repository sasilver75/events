package main

import (
	"math"
	"math/rand/v2"
	"time"
)

// CuratedEvent is the shape inserted into public.events with source='curated'.
// Each entry is keyed by SeedKey for ON CONFLICT idempotent upsert.
type CuratedEvent struct {
	SeedKey            string
	Title              string
	Description        string
	Category           string
	StartTime          time.Time
	EndTime            time.Time
	Cap                int
	CenterLat          float64
	CenterLon          float64
	LocationVisibility string   // 'fuzzed' | 'public'
	FuzzRadiusM        *int     // nil for 'public'
	DisplayLat         *float64 // nil for 'public'
	DisplayLon         *float64 // nil for 'public'
}

// curatedEvents returns the v0 hand-curated LA Events as of `now`. Start times
// are computed relative to `now` so re-running the seed on a later day still
// produces Events in the near future. The spread (10am, 4pm, 6pm next-day) was
// chosen to demonstrate browse filtering across morning/afternoon/evening
// windows; categories span Sports, Food/Drink, Outdoors to exercise the
// CONTEXT.md taxonomy on the iOS map. Visibility mix (1 public + 2 fuzzed)
// exercises both branches of the GET /events?near response.
func curatedEvents(now time.Time) []CuratedEvent {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		la = time.UTC
	}
	nowLA := now.In(la)

	tomorrow := nextLocalAt(nowLA, 1, 16, 0, la) // tomorrow 4:00pm LA
	morning := nextLocalAt(nowLA, 1, 10, 0, la)  // tomorrow 10:00am LA
	dayAfter := nextLocalAt(nowLA, 2, 18, 0, la) // day-after-tomorrow 6:00pm LA

	events := []CuratedEvent{
		{
			SeedKey:     "la-venice-beach-pickup-basketball",
			Title:       "Pickup basketball at Venice Beach",
			Description: "5v5 pickup at the Venice Beach courts on Ocean Front Walk. All skill levels welcome — call winners.",
			Category:    "Sports",
			StartTime:   tomorrow,
			EndTime:     tomorrow.Add(90 * time.Minute),
			Cap:         10,
			CenterLat:   33.9866,
			CenterLon:   -118.4715,
			// Public boardwalk courts — venue is the entire stretch, fuzzing has no
			// privacy value here. Exercises the 'public' branch of the response.
			LocationVisibility: "public",
		},
		{
			SeedKey:            "la-silverlake-intelligentsia-coffee",
			Title:              "Morning coffee at Intelligentsia Silver Lake",
			Description:        "Friendly coffee meetup at Intelligentsia on Sunset. Drop in, grab a drink, chat with whoever shows up.",
			Category:           "Food/Drink",
			StartTime:          morning,
			EndTime:            morning.Add(60 * time.Minute),
			Cap:                6,
			CenterLat:          34.0858,
			CenterLon:          -118.2710,
			LocationVisibility: "fuzzed",
		},
		{
			SeedKey:            "la-griffith-observatory-sunset-hike",
			Title:              "Sunset hike at Griffith Observatory",
			Description:        "Easy hike up the Mount Hollywood trail from the Observatory parking lot. Bring water and a layer for after dark.",
			Category:           "Outdoors",
			StartTime:          dayAfter,
			EndTime:            dayAfter.Add(2 * time.Hour),
			Cap:                8,
			CenterLat:          34.1184,
			CenterLon:          -118.3004,
			LocationVisibility: "fuzzed",
		},
	}

	radius := 200
	for i := range events {
		if events[i].LocationVisibility != "fuzzed" {
			continue
		}
		events[i].FuzzRadiusM = &radius
		lat, lon := fuzzedDisplayPoint(events[i].CenterLat, events[i].CenterLon, radius)
		events[i].DisplayLat = &lat
		events[i].DisplayLon = &lon
	}
	return events
}

// fuzzedDisplayPoint returns a point uniformly distributed within radiusM
// meters of (centerLat, centerLon). The set-once invariant lives at the DB
// upsert layer (COALESCE), so this is free to produce a fresh point each call.
func fuzzedDisplayPoint(centerLat, centerLon float64, radiusM int) (lat, lon float64) {
	r := float64(radiusM) * math.Sqrt(rand.Float64())
	theta := 2 * math.Pi * rand.Float64()

	dLatM := r * math.Sin(theta)
	dLonM := r * math.Cos(theta)

	const metersPerDegLat = 111320.0
	metersPerDegLon := 111320.0 * math.Cos(centerLat*math.Pi/180.0)

	lat = centerLat + dLatM/metersPerDegLat
	lon = centerLon + dLonM/metersPerDegLon
	return
}

// nextLocalAt returns a time `daysAhead` days from `from` at the given local
// hour:minute in `loc`.
func nextLocalAt(from time.Time, daysAhead, hour, minute int, loc *time.Location) time.Time {
	target := from.AddDate(0, 0, daysAhead)
	return time.Date(target.Year(), target.Month(), target.Day(), hour, minute, 0, 0, loc)
}
