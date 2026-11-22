package main

import "time"

// US Eastern timezone for market hours calculations.
var eastern *time.Location

func init() {
	var err error
	eastern, err = time.LoadLocation("America/New_York")
	if err != nil {
		// Fallback to a fixed offset if tzdata is not available.
		eastern = time.FixedZone("EST", -5*3600)
	}
}

// US market holidays for the current and next year. Updated annually.
var marketHolidays = map[string]bool{
	// 2026.
	"2026-01-01": true, // New Year's Day
	"2026-01-19": true, // MLK Day
	"2026-02-16": true, // Presidents' Day
	"2026-04-03": true, // Good Friday
	"2026-05-25": true, // Memorial Day
	"2026-06-19": true, // Juneteenth
	"2026-07-03": true, // Independence Day (observed)
	"2026-09-07": true, // Labor Day
	"2026-11-26": true, // Thanksgiving
	"2026-12-25": true, // Christmas
	// 2027.
	"2027-01-01": true,
	"2027-01-18": true,
	"2027-02-15": true,
	"2027-03-26": true,
	"2027-05-31": true,
	"2027-06-18": true,
	"2027-07-05": true,
	"2027-09-06": true,
	"2027-11-25": true,
	"2027-12-24": true,
}

// IsMarketOpen returns true if the US stock market is currently in regular trading hours.
func IsMarketOpen() bool {
	now := time.Now().In(eastern)

	// Weekends are closed.
	if now.Weekday() == time.Saturday || now.Weekday() == time.Sunday {
		return false
	}

	// Check holidays.
	dateStr := now.Format("2006-01-02")
	if marketHolidays[dateStr] {
		return false
	}

	// Regular hours: 9:30 AM - 4:00 PM Eastern.
	hour, min, _ := now.Clock()
	minuteOfDay := hour*60 + min
	return minuteOfDay >= 9*60+30 && minuteOfDay < 16*60
}

// NextOpenTime returns the next market open time for sleep scheduling.
func NextOpenTime() time.Time {
	now := time.Now().In(eastern)
	candidate := time.Date(now.Year(), now.Month(), now.Day(), 9, 30, 0, 0, eastern)

	// If we're past today's open, start checking from tomorrow.
	if now.After(candidate) || now.Equal(candidate) {
		candidate = candidate.AddDate(0, 0, 1)
	}

	// Skip weekends and holidays.
	for {
		if candidate.Weekday() == time.Saturday {
			candidate = candidate.AddDate(0, 0, 2)
			continue
		}
		if candidate.Weekday() == time.Sunday {
			candidate = candidate.AddDate(0, 0, 1)
			continue
		}
		dateStr := candidate.Format("2006-01-02")
		if marketHolidays[dateStr] {
			candidate = candidate.AddDate(0, 0, 1)
			continue
		}
		break
	}

	return candidate
}
