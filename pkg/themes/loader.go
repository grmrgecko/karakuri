package themes

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grmrgecko/karakuri/internal/resources"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// ThemeManifest is the parsed representation of a theme's manifest.yaml.
type ThemeManifest struct {
	Name        string            `yaml:"name"`
	PackageName string            `yaml:"packageName"`
	Version     string            `yaml:"version"`
	Description string            `yaml:"description"`
	Author      string            `yaml:"author"`
	Copyright   string            `yaml:"copyright"`
	License     string            `yaml:"license"`
	Variables   map[string]string `yaml:"variables"`
}

// LoadAndRegister scans all theme resource directories for manifest.yaml files,
// upserts each theme record in the database, and removes any
// database entries whose package is no longer on disk.
func LoadAndRegister(log *zap.Logger) error {
	paths := resources.GetResourcePaths(resources.Themes)
	discovered := make(map[string]bool)

	for _, basePath := range paths {
		entries, err := os.ReadDir(basePath)
		if err != nil {
			log.Warn("failed to read theme directory", zap.String("path", basePath), zap.Error(err))
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}

			manifestPath := filepath.Join(basePath, entry.Name(), "manifest.yaml")
			if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
				continue
			}

			manifest, err := parseThemeManifest(manifestPath)
			if err != nil {
				log.Warn("failed to parse theme manifest",
					zap.String("path", manifestPath),
					zap.Error(err))
				continue
			}

			if err := registerTheme(manifest); err != nil {
				log.Warn("failed to register theme",
					zap.String("package", manifest.PackageName),
					zap.Error(err))
				continue
			}

			discovered[manifest.PackageName] = true
			log.Info("registered theme", zap.String("package", manifest.PackageName))
		}
	}

	// Remove database entries for themes no longer present on disk. An empty
	// scan more likely means a missing resource directory than a deliberate
	// removal of every package, so leave the registry alone in that case.
	if len(discovered) > 0 {
		removeStaleThemes(log, discovered)
	}

	return nil
}

// removeStaleThemes deletes theme records whose package name was not found during scanning.
func removeStaleThemes(log *zap.Logger, discovered map[string]bool) {
	var themes []models.Theme
	if err := db.DB.Find(&themes).Error; err != nil {
		log.Warn("failed to list themes for cleanup", zap.Error(err))
		return
	}

	for _, t := range themes {
		if !discovered[t.PackageName] {
			log.Info("removing stale theme", zap.String("package", t.PackageName))
			if err := db.DB.Delete(&t).Error; err != nil {
				log.Warn("failed to delete stale theme", zap.String("package", t.PackageName), zap.Error(err))
			}
		}
	}
}

func parseThemeManifest(path string) (*ThemeManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest ThemeManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &manifest, nil
}

func registerTheme(m *ThemeManifest) error {
	varsJSON, _ := json.Marshal(m.Variables)

	var existing models.Theme
	result := db.DB.Where("package_name = ?", m.PackageName).First(&existing)

	if result.Error == nil {
		existing.Name = m.Name
		existing.Version = m.Version
		existing.Description = m.Description
		existing.Author = m.Author
		existing.Copyright = m.Copyright
		existing.License = m.License
		existing.Variables = string(varsJSON)
		return db.DB.Save(&existing).Error
	}

	theme := models.Theme{
		Name:        m.Name,
		PackageName: m.PackageName,
		Version:     m.Version,
		Description: m.Description,
		Author:      m.Author,
		Copyright:   m.Copyright,
		License:     m.License,
		Variables:   string(varsJSON),
	}

	return db.DB.Create(&theme).Error
}
