package main

import "time"

// CuratedEvent is the shape inserted into public.events with source='curated'.
// Each entry is keyed by SeedKey for ON CONFLICT idempotent upsert.
type CuratedEvent struct {
	SeedKey         string
	Title           string
	Description     string
	Category        string
	StartAt         time.Time
	DurationMinutes int
	Cap             int
	CenterLat       float64
	CenterLon       float64
}

// curatedEvents returns the v0 hand-curated LA Events as of `now`. Start times
// are computed relative to `now` so re-running the seed on a later day still
// produces Events in the near future. The spread (10am, 4pm, 6pm next-day) was
// chosen to demonstrate browse filtering across morning/afternoon/evening
// windows; categories span Sports, Food/Drink, Outdoors to exercise the
// CONTEXT.md taxonomy on the iOS map.
func curatedEvents(now time.Time) []CuratedEvent {
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		la = time.UTC
	}
	nowLA := now.In(la)

	tomorrow := nextLocalAt(nowLA, 1, 16, 0, la) // tomorrow 4:00pm LA
	morning := nextLocalAt(nowLA, 1, 10, 0, la)  // tomorrow 10:00am LA
	dayAfter := nextLocalAt(nowLA, 2, 18, 0, la) // day-after-tomorrow 6:00pm LA

	return []CuratedEvent{
		{
			// Pickup basketball at the Venice Beach courts — public, well-known
			// outdoor courts on Ocean Front Walk. High-visibility default Cap of
			// 10 (5v5) matches the venue's natural game shape.
			SeedKey:         "la-venice-beach-pickup-basketball",
			Title:           "Pickup basketball at Venice Beach",
			Description:     "5v5 pickup at the Venice Beach courts on Ocean Front Walk. All skill levels welcome — call winners.",
			Category:        "Sports",
			StartAt:         tomorrow,
			DurationMinutes: 90,
			Cap:             10,
			CenterLat:       33.9866,
			CenterLon:       -118.4715,
		},
		{
			// Coffee meetup at Intelligentsia Silver Lake — real cafe on Sunset
			// Blvd, intentionally low Cap (6) to keep it intimate and to test the
			// small-Cap UI path. Morning slot complements the afternoon Sports
			// Event on the browse map.
			SeedKey:         "la-silverlake-intelligentsia-coffee",
			Title:           "Morning coffee at Intelligentsia Silver Lake",
			Description:     "Friendly coffee meetup at Intelligentsia on Sunset. Drop in, grab a drink, chat with whoever shows up.",
			Category:        "Food/Drink",
			StartAt:         morning,
			DurationMinutes: 60,
			Cap:             6,
			CenterLat:       34.0858,
			CenterLon:       -118.2710,
		},
		{
			// Sunset hike from Griffith Observatory along the Mount Hollywood
			// Trail. Outdoors category, mid-Cap (8). Evening start exercises the
			// "next 72 hours" browse window past the simple "tomorrow" axis.
			SeedKey:         "la-griffith-observatory-sunset-hike",
			Title:           "Sunset hike at Griffith Observatory",
			Description:     "Easy hike up the Mount Hollywood trail from the Observatory parking lot. Bring water and a layer for after dark.",
			Category:        "Outdoors",
			StartAt:         dayAfter,
			DurationMinutes: 120,
			Cap:             8,
			CenterLat:       34.1184,
			CenterLon:       -118.3004,
		},
	}
}

// nextLocalAt returns a time `daysAhead` days from `from` at the given local
// hour:minute in `loc`.
func nextLocalAt(from time.Time, daysAhead, hour, minute int, loc *time.Location) time.Time {
	target := from.AddDate(0, 0, daysAhead)
	return time.Date(target.Year(), target.Month(), target.Day(), hour, minute, 0, 0, loc)
}
