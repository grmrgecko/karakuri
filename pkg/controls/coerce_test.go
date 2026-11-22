package controls_test

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/stretchr/testify/assert"
)

// TestCoerceNumberRejectsNonFiniteText locks that a number control never holds
// NaN or an infinity. strconv.ParseFloat reads "nan" and "inf" in any case,
// and sensor firmware publishes "nan" for a failed read. NaN passes every
// range check because both comparisons are false, then cannot be stored or
// published because JSON has no spelling for it.
func TestCoerceNumberRejectsNonFiniteText(t *testing.T) {
	opts := map[string]any{"min": 0.0, "max": 100.0}
	for _, text := range []string{"NaN", "nan", "Inf", "-Infinity"} {
		_, err := controls.Coerce(controls.KindNumber, opts, text)
		assert.Errorf(t, err, "%q is not a value a number control can hold", text)
	}

	got, err := controls.Coerce(controls.KindNumber, opts, " 21.5 ")
	assert.NoError(t, err, "numeric text still coerces")
	assert.Equal(t, 21.5, got, "numeric text becomes its number")
}
