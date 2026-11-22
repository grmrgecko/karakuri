package main

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/internal/resources"
	"github.com/grmrgecko/karakuri/internal/service"
	"github.com/grmrgecko/karakuri/internal/version"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/migrate"
)

// VersionCmd prints the build version and exits.
type VersionCmd struct{}

func (c *VersionCmd) Run() error {
	fmt.Printf("%s %s\n", version.Name, version.String())
	return nil
}

// CLI defines the top-level command tree for the karakuri binary.
type CLI struct {
	Serve       ServeCmd       `cmd:"" help:"Start the web server."`
	User        UserCmd        `cmd:"" help:"User management commands."`
	Config      ConfigCmd      `cmd:"" help:"Configuration commands."`
	Service     ServiceCmd     `cmd:"" help:"Install and control karakuri as a system service."`
	Token       TokenCmd       `cmd:"" help:"API bearer-token commands."`
	Backup      BackupCmd      `cmd:"" help:"Back up and restore this installation."`
	Update      UpdateCmd      `cmd:"" help:"Check for a newer release and install it."`
	Healthcheck HealthcheckCmd `cmd:"" help:"Exit zero when the server on this host is serving, for container healthchecks."`
	Version     VersionCmd     `cmd:"" help:"Show version information."`
}

// dbOptions builds database options from the current config.
func dbOptions() db.Options {
	return db.Options{
		Type:            config.C.Database.Type,
		DSN:             config.C.Database.DSN,
		MaxOpenConns:    config.C.Database.MaxOpenConns,
		MaxIdleConns:    config.C.Database.MaxIdleConns,
		ConnMaxLifetime: config.C.Database.ConnMaxLifetime,
		LogLevel:        config.C.Logging.Level,
	}
}

// initDatabase connects to the database, runs migrations, and seeds defaults.
func initDatabase() error { return initDatabaseWith(dbOptions()) }

// initDatabaseQuiet connects with SQL logging suppressed, for commands whose
// own stdout is the operator-facing result and must not be buried in queries.
func initDatabaseQuiet() error {
	opts := dbOptions()
	opts.LogLevel = "silent"
	return initDatabaseWith(opts)
}

// initDatabaseWith performs the connect, migrate, and seed sequence.
func initDatabaseWith(opts db.Options) error {
	if err := db.Init(opts); err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}

	// Refuse an older release, and copy the database before a newer one
	// migrates it.
	if err := prepareUpgrade(startLog, resources.GetDataPath()); err != nil {
		return err
	}

	if err := migrate.AutoMigrate(); err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	if err := migrate.SeedDefaults(); err != nil {
		return fmt.Errorf("failed to seed defaults: %w", err)
	}

	if err := migrate.BackfillDashboardDeployments(); err != nil {
		return fmt.Errorf("failed to backfill dashboard versions: %w", err)
	}

	if err := migrate.BackfillControlKinds(); err != nil {
		return fmt.Errorf("failed to backfill control kinds: %w", err)
	}

	if err := migrate.BackfillAIProviderKind(); err != nil {
		return fmt.Errorf("failed to backfill AI provider kind: %w", err)
	}

	if err := migrate.BackfillAssistResponsePort(); err != nil {
		return fmt.Errorf("failed to backfill HA Assist response ports: %w", err)
	}

	return nil
}

// main runs the program.
func main() {
	// Setup the CLI.
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("karakuri"),
		kong.Description("Karakuri Home Automation Flow Engine and Dashboard"),
		kong.UsageOnError(),
		kong.Vars{"serviceActions": strings.Join(service.Actions, ",")},
	)

	// Read the config file.
	if err := config.Load(); err != nil {
		ctx.FatalIfErrorf(fmt.Errorf("failed to load config: %w", err))
	}

	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
