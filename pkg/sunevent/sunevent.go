// Package sunevent computes the times of solar events — sunrise, sunset, the
// twilights, and solar noon — for a location and date. Home automation runs on
// these as much as it runs on the clock: lights follow dusk, not 18:00.
//
// The formulas are the NOAA solar position algorithm, accurate to well under a
// minute for the latitudes people live at. Everything is computed in UTC; the
// caller applies whatever time zone it cares about.
package sunevent

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// Event names one solar event.
type Event string

// The supported events. The twilight pairs use the standard solar elevations:
// civil -6°, nautical -12°, astronomical -18°. Sunrise and sunset use -0.833°,
// which accounts for atmospheric refraction and the sun's radius, so the event
// is the moment the upper edge meets the horizon.
const (
	Sunrise          Event = "sunrise"
	Sunset           Event = "sunset"
	SolarNoon        Event = "solar_noon"
	Dawn             Event = "dawn"
	Dusk             Event = "dusk"
	NauticalDawn     Event = "nautical_dawn"
	NauticalDusk     Event = "nautical_dusk"
	AstronomicalDawn Event = "astronomical_dawn"
	AstronomicalDusk Event = "astronomical_dusk"
	// CustomElevation resolves against a caller-supplied elevation angle, which
	// covers everything the named events do not (golden hour at +6°, a blue
	// hour at -4°, a panel that only matters above +15°).
	CustomElevation Event = "elevation"
)

// eventSpec is how an event maps onto the underlying calculation: the solar
// elevation to solve for, and which side of solar noon to take.
type eventSpec struct {
	elevation float64
	morning   bool
	noon      bool
}

// specs describes every named event.
var specs = map[Event]eventSpec{
	Sunrise:          {elevation: -0.833, morning: true},
	Sunset:           {elevation: -0.833, morning: false},
	SolarNoon:        {noon: true},
	Dawn:             {elevation: -6, morning: true},
	Dusk:             {elevation: -6, morning: false},
	NauticalDawn:     {elevation: -12, morning: true},
	NauticalDusk:     {elevation: -12, morning: false},
	AstronomicalDawn: {elevation: -18, morning: true},
	AstronomicalDusk: {elevation: -18, morning: false},
}

// ErrNoEvent reports that the event does not happen on the requested date. Above
// the polar circles the sun can stay up or down for months, so this is a normal
// answer rather than a failure.
type ErrNoEvent struct {
	Event Event
	Date  time.Time
}

func (e *ErrNoEvent) Error() string {
	return fmt.Sprintf("no %s at this location on %s", e.Event, e.Date.Format("2006-01-02"))
}

// Options describes one lookup.
type Options struct {
	Event Event
	// Latitude in degrees, positive north.
	Latitude float64
	// Longitude in degrees, positive east.
	Longitude float64
	// Elevation is the solar elevation in degrees, used only by CustomElevation.
	Elevation float64
	// Morning selects the ascending crossing for CustomElevation; false takes
	// the descending one.
	Morning bool
}

// ParseEvent resolves an event name, accepting the aliases people reach for.
func ParseEvent(s string) (Event, error) {
	switch ev := Event(strings.ToLower(strings.TrimSpace(s))); ev {
	case Sunrise, Sunset, SolarNoon, Dawn, Dusk,
		NauticalDawn, NauticalDusk, AstronomicalDawn, AstronomicalDusk, CustomElevation:
		return ev, nil
	case "civil_dawn":
		return Dawn, nil
	case "civil_dusk":
		return Dusk, nil
	case "noon":
		return SolarNoon, nil
	default:
		return "", fmt.Errorf("unknown sun event %q", s)
	}
}

// Validate checks the options make sense before they are used to arm a trigger.
func (o Options) Validate() error {
	if o.Latitude < -90 || o.Latitude > 90 {
		return fmt.Errorf("latitude %g is outside -90..90", o.Latitude)
	}
	if o.Longitude < -180 || o.Longitude > 180 {
		return fmt.Errorf("longitude %g is outside -180..180", o.Longitude)
	}
	if o.Event == CustomElevation {
		if o.Elevation < -90 || o.Elevation > 90 {
			return fmt.Errorf("elevation %g is outside -90..90", o.Elevation)
		}
		return nil
	}
	if _, ok := specs[o.Event]; !ok {
		return fmt.Errorf("unknown sun event %q", o.Event)
	}
	return nil
}

// spec resolves the options to a calculation target.
func (o Options) spec() eventSpec {
	if o.Event == CustomElevation {
		return eventSpec{elevation: o.Elevation, morning: o.Morning}
	}
	return specs[o.Event]
}

// On returns the UTC time of the event for the UTC date containing t. It returns
// *ErrNoEvent when the sun never reaches the required elevation that day.
func On(t time.Time, o Options) (time.Time, error) {
	if err := o.Validate(); err != nil {
		return time.Time{}, err
	}
	day := t.UTC().Truncate(24 * time.Hour)
	sp := o.spec()

	// Solve iteratively: the sun's declination and the equation of time are
	// evaluated at the estimated event time, so a second pass off a noon guess
	// converges to well under a second.
	//
	// The noon guess fails on the last date before the sun stops crossing the
	// elevation: the event falls near solar midnight, half a day from noon, and
	// the declination has moved far enough by noon that the crossing no longer
	// exists there. The solve then starts over from the solar midnight on the
	// event's side of noon, where the crossing still does.
	//
	// The first guess is mean solar noon rather than 12:00 UTC. Far from the
	// prime meridian the two are hours apart, which is enough to lose the first
	// event after a polar night: it happens at solar noon and nowhere else.
	offset := time.Duration((720 - 4*o.Longitude) * float64(time.Minute))
	passes, nearMidnight := 3, false
	for i := 0; i < passes; i++ {
		century := julianCentury(day.Add(offset))
		declination := sunDeclination(century)
		noonMinutes := 720 - 4*o.Longitude - equationOfTime(century)

		minutes := noonMinutes
		if !sp.noon {
			ha, err := hourAngle(o.Latitude, declination, 90-sp.elevation)
			if err != nil {
				if i > 0 || nearMidnight {
					return time.Time{}, &ErrNoEvent{Event: o.Event, Date: day}
				}
				nearMidnight = true
				ha = 180
				// The first estimate sits half a day from the first guess, so it
				// takes one more pass to settle.
				passes++
			}
			if sp.morning {
				minutes -= 4 * ha
			} else {
				minutes += 4 * ha
			}
		}
		offset = time.Duration(minutes * float64(time.Minute))
	}
	return day.Add(offset), nil
}

// maxSearchDays bounds how far Next looks ahead. Inside the polar circles a
// twilight event can be months away, and beyond that the answer is that it never
// happens.
const maxSearchDays = 400

// Next returns the first occurrence of the event strictly after t, with offset
// applied to the computed time. A negative offset asks for "before sunset".
func Next(t time.Time, o Options, offset time.Duration) (time.Time, error) {
	if err := o.Validate(); err != nil {
		return time.Time{}, err
	}
	// Start a day early so an event later today that the offset pushes past t is
	// not skipped. A positive offset carries the event of an older date past t
	// as well, so the start moves back by the days the offset spans.
	back := 1
	if offset > 0 {
		back += int(offset/(24*time.Hour)) + 1
	}
	day := t.UTC().Truncate(24*time.Hour).AddDate(0, 0, -back)
	for i := 0; i < maxSearchDays+back; i++ {
		at, err := On(day.AddDate(0, 0, i), o)
		if err != nil {
			// No event on this date; the next one may still come.
			continue
		}
		if fired := at.Add(offset); fired.After(t) {
			return fired, nil
		}
	}
	return time.Time{}, fmt.Errorf("no %s occurs at this location within %d days", o.Event, maxSearchDays)
}

// julianCentury converts a time to Julian centuries since J2000.0, the time
// base every term below is expressed in.
func julianCentury(t time.Time) float64 {
	julianDay := float64(t.UTC().UnixNano())/float64(24*time.Hour) + 2440587.5
	return (julianDay - 2451545.0) / 36525.0
}

// sunDeclination returns the sun's declination in degrees.
func sunDeclination(century float64) float64 {
	apparentLong := sunApparentLongitude(century)
	obliquity := obliquityCorrection(century)
	return degrees(math.Asin(math.Sin(radians(obliquity)) * math.Sin(radians(apparentLong))))
}

// equationOfTime returns the difference between apparent and mean solar time in
// minutes: the reason solar noon drifts through the year.
func equationOfTime(century float64) float64 {
	obliquity := obliquityCorrection(century)
	meanLong := geomMeanLongitude(century)
	meanAnomaly := geomMeanAnomaly(century)
	eccentricity := orbitEccentricity(century)

	y := math.Tan(radians(obliquity/2)) * math.Tan(radians(obliquity/2))
	return 4 * degrees(
		y*math.Sin(2*radians(meanLong))-
			2*eccentricity*math.Sin(radians(meanAnomaly))+
			4*eccentricity*y*math.Sin(radians(meanAnomaly))*math.Cos(2*radians(meanLong))-
			0.5*y*y*math.Sin(4*radians(meanLong))-
			1.25*eccentricity*eccentricity*math.Sin(2*radians(meanAnomaly)))
}

// hourAngle returns the hour angle in degrees at which the sun reaches the given
// zenith. An out-of-range cosine means the sun never gets there on this date.
func hourAngle(latitude, declination, zenith float64) (float64, error) {
	cosHA := (math.Cos(radians(zenith)) / (math.Cos(radians(latitude)) * math.Cos(radians(declination)))) -
		math.Tan(radians(latitude))*math.Tan(radians(declination))
	if cosHA > 1 || cosHA < -1 {
		return 0, fmt.Errorf("the sun does not reach a zenith of %g degrees", zenith)
	}
	return degrees(math.Acos(cosHA)), nil
}

// geomMeanLongitude returns the sun's geometric mean longitude in degrees.
func geomMeanLongitude(century float64) float64 {
	return math.Mod(280.46646+century*(36000.76983+century*0.0003032), 360)
}

// geomMeanAnomaly returns the sun's geometric mean anomaly in degrees.
func geomMeanAnomaly(century float64) float64 {
	return 357.52911 + century*(35999.05029-0.0001537*century)
}

// orbitEccentricity returns the eccentricity of Earth's orbit.
func orbitEccentricity(century float64) float64 {
	return 0.016708634 - century*(0.000042037+0.0000001267*century)
}

// sunEqOfCenter returns the equation of center in degrees, the correction from
// the mean to the true anomaly.
func sunEqOfCenter(century float64) float64 {
	m := radians(geomMeanAnomaly(century))
	return math.Sin(m)*(1.914602-century*(0.004817+0.000014*century)) +
		math.Sin(2*m)*(0.019993-0.000101*century) +
		math.Sin(3*m)*0.000289
}

// sunApparentLongitude returns the sun's apparent longitude in degrees,
// including the nutation and aberration corrections.
func sunApparentLongitude(century float64) float64 {
	trueLong := geomMeanLongitude(century) + sunEqOfCenter(century)
	return trueLong - 0.00569 - 0.00478*math.Sin(radians(125.04-1934.136*century))
}

// obliquityCorrection returns the corrected obliquity of the ecliptic in degrees.
func obliquityCorrection(century float64) float64 {
	mean := 23 + (26+(21.448-century*(46.815+century*(0.00059-century*0.001813)))/60)/60
	return mean + 0.00256*math.Cos(radians(125.04-1934.136*century))
}

// radians converts degrees to radians.
func radians(deg float64) float64 { return deg * math.Pi / 180 }

// degrees converts radians to degrees.
func degrees(rad float64) float64 { return rad * 180 / math.Pi }
