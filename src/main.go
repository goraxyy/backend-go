package main

import (
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	_ "github.com/prometheus/client_golang/prometheus/promhttp"
	"log/slog"
	"net/http"
	"os"
	"spacesona-go-backend/admin"
	"spacesona-go-backend/auth"
	"spacesona-go-backend/db"
	"spacesona-go-backend/helpers"
	"spacesona-go-backend/info"
	"spacesona-go-backend/logging"
	"spacesona-go-backend/migrations"
	"spacesona-go-backend/status"
)

func main() {
	// A .env file is a local-development convenience, not a requirement:
	// containers and systemd units supply configuration as real environment
	// variables. The previous code treated a missing file as fatal and then
	// entered an empty `for {}` loop, which pins a CPU core forever while the
	// process never binds a port -- so `restart: always` cannot recover it and
	// any health check that only pings the container reports healthy.
	if envErr := godotenv.Load(); envErr != nil {
		slog.Info("no .env file found, using environment variables")
	}

	// Fail fast and loudly on missing configuration rather than starting up
	// with an empty JWT_SECRET, which would make every token forgeable.
	for _, required := range []string{"JWT_SECRET", "DRIVER", "DB_URL", "REDIS_URL"} {
		if os.Getenv(required) == "" {
			slog.Error("required environment variable is not set", "variable", required)
			os.Exit(1)
		}
	}
	db.Database = db.New()

	// MIGRATE runs migrations/0-init.sql, which begins with five DROP TABLE
	// statements. Leaving it set in a deployed .env destroys every board,
	// machine, client and status record on each restart, so it is logged
	// loudly and no longer drags dummy data in with it.
	if os.Getenv("MIGRATE") == "true" {
		slog.Warn("MIGRATE=true: applying 0-init.sql, which DROPS ALL EXISTING TABLES")
		migrations.Migrate()
	}
	// Seeding fictional Lafayette and WPI records used to be unconditional
	// whenever MIGRATE was set. It now needs its own opt-in.
	if os.Getenv("SEED_DUMMY_DATA") == "true" {
		slog.Warn("SEED_DUMMY_DATA=true: inserting sample clients, buildings and boards")
		migrations.DummyData()
	}
	deviceAuthRoute := auth.Route[string]{
		Authenticate: auth.AuthenticateDevice,
		OnError:      auth.AuthHttpError,
		WriteToken:   []func(w http.ResponseWriter, r *http.Request, token string){auth.WriteTokenToAuthHeader, auth.WriteTokenToBody},
	}
	userAuthCallBack := auth.Route[*auth.SpaceonaUserToken]{
		Authenticate: auth.AuthenticateSpaceonaUser,
		OnError:      auth.AuthHttpError,
		WriteToken:   []func(w http.ResponseWriter, r *http.Request, token *auth.SpaceonaUserToken){auth.WriteSpaceonaTokenToCooke, auth.Redirect},
	}
	http.Handle("/", helpers.CorsMiddleware(info.APIInfoRoute))
	http.Handle("/firmware/file/{version}", auth.Middleware(logging.Middleware(helpers.CorsMiddleware(FileRoute))))
	http.Handle("/firmware/latest", auth.Middleware(logging.Middleware(helpers.CorsMiddleware(LatestVersionRoute))))
	http.Handle("/auth/device", logging.Middleware(deviceAuthRoute))
	http.Handle("/auth/user", logging.Middleware(http.HandlerFunc(auth.GoogleConsent)))
	http.Handle("/auth/user/callback", logging.Middleware(userAuthCallBack))
	http.Handle("/status/update", auth.Middleware(logging.Middleware(helpers.CorsMiddleware(status.UpdateStatusRoute))))
	// /onboard/board stays open to unauthenticated callers by design: a factory
	// -fresh board has no token yet. It is gated by the client key instead,
	// which is now actually enforced (see admin.BoardOnboardingRoute).
	http.Handle("/onboard/board", logging.Middleware(helpers.CorsMiddleware(admin.BoardOnboardingRoute)))

	// Everything below mutates fleet state or mints client keys, so it requires
	// the operator secret. These previously had logging.Middleware applied
	// twice where the outer wrapper should have been an auth check, leaving
	// them open to anyone on the internet.
	http.Handle("/onboard/client", auth.AdminMiddleware(logging.Middleware(helpers.CorsMiddleware(admin.ClientOnboardingRoute))))
	http.Handle("/admin/machine/assign", auth.AdminMiddleware(logging.Middleware(helpers.CorsMiddleware(admin.AssignBoardEndpoint))))
	http.Handle("/admin/device/valid", auth.AdminMiddleware(logging.Middleware(helpers.CorsMiddleware(admin.SetBoardValid))))
	http.Handle("/admin/machine/new", auth.AdminMiddleware(logging.Middleware(helpers.CorsMiddleware(admin.AddNewMachines))))
	http.Handle("/status/", logging.Middleware(helpers.CorsMiddleware(status.GetStatusRoute)))
	http.Handle("/status/{client}", logging.Middleware(helpers.CorsMiddleware(status.GetStatusRoute)))
	http.Handle("/status/{client}/{building}", logging.Middleware(helpers.CorsMiddleware(status.GetStatusRoute)))
	http.Handle("/status/{client}/{building}/{type}", logging.Middleware(helpers.CorsMiddleware(status.GetStatusRoute)))
	http.Handle("/status/{client}/{building}/{type}/{machineId}", logging.Middleware(helpers.CorsMiddleware(status.GetStatusRoute)))
	http.Handle("/info/client/{client}", logging.Middleware(helpers.CorsMiddleware(info.GetClientInfoRoute)))
	http.Handle("/info/board/{client}", logging.Middleware(helpers.CorsMiddleware(info.GetBoardInfoRoute)))
	http.Handle("/metrics", promhttp.Handler())
	port := os.Getenv("PORT")
	if port == "" {
		port = "3001"
	}
	slog.Info("listening", "port", port)
	if startUpErr := http.ListenAndServe(":"+port, nil); startUpErr != nil {
		panic(startUpErr)
	}
}
