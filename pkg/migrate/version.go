package migrate

import (
	"errors"
	"fmt"

	"github.com/Masterminds/semver/v3"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"gorm.io/gorm"
)

// VersionSetting holds the release that last started healthy on this
// database. It is written once the server serves, so a release that never
// comes up does not claim the database, and an install put back to its
// previous release still starts.
const VersionSetting = "server.version"

// AvailableSetting holds the newest release a check found that this server
// has not installed. The overview reports it while it is newer than the
// running release.
const AvailableSetting = "update.available"

// StoredVersion returns the release last recorded on the database, or ""
// for a new database or one last used by a release from before versions were
// recorded. It runs before migrations, so it reads only what every release
// has: the settings table.
func StoredVersion() (string, error) {
	if !db.DB.Migrator().HasTable(&models.Setting{}) {
		return "", nil
	}
	s, err := models.GetSetting(VersionSetting)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading the recorded version: %w", err)
	}
	return s.Value, nil
}

// RecordVersion stores v as the release that last ran. Builds without a
// release version are not recorded, so a development build never stops a
// release from starting.
func RecordVersion(v string) error {
	if _, ok := parseRelease(v); !ok {
		return nil
	}
	return models.SetSetting(VersionSetting, v)
}

// IsRelease reports whether v is a release version rather than a
// development build.
func IsRelease(v string) bool {
	_, ok := parseRelease(v)
	return ok
}

// CompareVersions orders two release versions: negative when a is older than
// b, zero when they are the same release, positive when a is newer. ok is
// false when either is not a release version, such as "dev", in which case
// they are not compared.
func CompareVersions(a, b string) (cmp int, ok bool) {
	va, aok := parseRelease(a)
	vb, bok := parseRelease(b)
	if !aok || !bok {
		return 0, false
	}
	return va.Compare(vb), true
}

// parseRelease reads a semantic version, with or without a leading v.
func parseRelease(v string) (*semver.Version, bool) {
	if v == "" {
		return nil, false
	}
	parsed, err := semver.NewVersion(v)
	if err != nil {
		return nil, false
	}
	return parsed, true
}
