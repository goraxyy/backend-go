package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"spacesona-go-backend/db"
	"time"
)

// ErrDeviceUnauthorized is returned for every authentication failure.
//
// Callers must not distinguish "no such board", "board revoked" and "wrong
// key" to the client. Doing so turns the endpoint into an oracle that confirms
// which MAC addresses exist.
var ErrDeviceUnauthorized = errors.New("device authentication failed")

type AuthDeviceRequest struct {
	MacAddress      string `json:"mac_address"`
	FirmwareVersion string `json:"firmware_version"`
	// ClientKey is the shared secret the firmware already sends. It was
	// previously absent from this struct, so encoding/json silently discarded
	// it and the endpoint authenticated on MAC address alone.
	ClientKey string `json:"client_key"`
}

type AuthUserRequest struct {
}

// AuthenticateDevice issues a device token only for a board that exists, is
// still marked valid, and presents the correct key for its owning client.
func AuthenticateDevice(r *http.Request) (string, error) {
	var request AuthDeviceRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&request)
	if decodeErr != nil {
		return "", decodeErr
	}
	if request.MacAddress == "" || request.ClientKey == "" {
		return "", ErrDeviceUnauthorized
	}

	// One query for the board and the client that owns it. The previous
	// version selected `valid` and never read it.
	querySqlString := `SELECT board.valid, client.key, client.salt
	                   FROM board
	                   JOIN client ON client.name = board.client_name
	                   WHERE board.mac_address = ?;`
	row, queryErr := db.UseSQL().Query(querySqlString, request.MacAddress)
	if queryErr != nil {
		slog.Error("device auth query failed", "error", queryErr.Error())
		return "", ErrDeviceUnauthorized
	}
	defer func(row *sql.Rows) {
		if closeErr := row.Close(); closeErr != nil {
			slog.Error("failed to close row", "error", closeErr.Error())
		}
	}(row)

	if !row.Next() {
		slog.Warn("device auth for unknown board", "mac address", request.MacAddress)
		return "", ErrDeviceUnauthorized
	}

	var valid bool
	var storedKey sql.NullString
	var salt []byte
	if scanErr := row.Scan(&valid, &storedKey, &salt); scanErr != nil {
		slog.Error("device auth scan failed", "error", scanErr.Error())
		return "", ErrDeviceUnauthorized
	}

	if !valid {
		slog.Warn("device auth for revoked board", "mac address", request.MacAddress)
		return "", ErrDeviceUnauthorized
	}
	if !storedKey.Valid {
		slog.Error("client has no key on record", "mac address", request.MacAddress)
		return "", ErrDeviceUnauthorized
	}

	keyMatches, compareErr := SecureCompareKey(storedKey.String, request.ClientKey, salt)
	if compareErr != nil {
		slog.Error("device auth key comparison failed", "error", compareErr.Error())
		return "", ErrDeviceUnauthorized
	}
	if !keyMatches {
		slog.Warn("device auth with wrong client key", "mac address", request.MacAddress)
		return "", ErrDeviceUnauthorized
	}

	// Only non-secret identity goes into the token. The previous version
	// embedded the whole decoded request, so adding ClientKey to that struct
	// would have published the shared secret inside every JWT.
	claims := DeviceClaims{
		Type:            TokenTypeDevice,
		MacAddress:      request.MacAddress,
		FirmwareVersion: request.FirmwareVersion,
	}
	return GenToken(claims, 24*time.Hour)
}
