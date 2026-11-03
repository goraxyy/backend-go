package status

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"spacesona-go-backend/auth"
	"spacesona-go-backend/db"
	"spacesona-go-backend/helpers"
	"spacesona-go-backend/logging"
	"time"
)

type UpdateStatusRequest struct {
	// MacAddress is accepted for wire compatibility with deployed firmware but
	// is NOT trusted. The board being written to is taken from the token.
	MacAddress        string `json:"mac_address"`
	FirmwareVersion   string `json:"firmwareVersion"`
	Status            bool   `json:"Status"`
	StatusChanged     bool   `json:"StatusChanged"`
	TimeBetweenChange int    `json:"timeBetweenChange"`
	Confidence        int    `json:"Confidence"`
}

var NotMappedError = errors.New("board is not mapped to machine")

func UpdateStatusRoute(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		logging.UpdateStatusDuration.Observe(time.Since(start).Seconds())
	}()
	if r.Method != "POST" {
		http.NotFound(w, r)
		return
	}

	// The MAC in the body used to select which machine got written. Any board
	// holding any valid token could therefore write every other board's
	// status. Identity now comes from the verified token instead.
	device, hasDevice := auth.DeviceFromContext(r.Context())
	if !hasDevice {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var body UpdateStatusRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&body)
	if decodeErr != nil {
		slog.Error(decodeErr.Error())
		http.Error(w, "cant decode request", http.StatusBadRequest)
		return
	}
	if body.MacAddress != "" && body.MacAddress != device.MacAddress {
		slog.Warn("board tried to write another board's status",
			"token mac", device.MacAddress, "body mac", body.MacAddress)
		http.Error(w, "mac address does not match credentials", http.StatusForbidden)
		return
	}
	body.MacAddress = device.MacAddress

	slog.Info("got request from board", "mac address", body.MacAddress, "firmware version", body.FirmwareVersion)
	if body.StatusChanged == true {
		updateErr := updateStatus(body)
		if updateErr != nil {
			slog.Error(updateErr.Error())
			if errors.Is(updateErr, NotMappedError) {
				http.Error(w, "board is not mapped to a machine contact admin", http.StatusBadRequest)
				return
			}
			http.Error(w, "failed to update status", http.StatusInternalServerError)
			return
		}
	} else {
		refreshErr := refreshCache(body)
		if refreshErr != nil {
			slog.Error(refreshErr.Error())
			http.Error(w, "failed to update status", http.StatusInternalServerError)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
	w.Header().Set("Content-Type", "application/json")
	_, WriteErr := w.Write([]byte("{message:\"updatedDB\"}"))
	if WriteErr != nil {
		http.Error(w, WriteErr.Error(), http.StatusInternalServerError)
		return
	}
	//TODO idk make a body or something
	return
}

func refreshCache(request UpdateStatusRequest) error {
	sqlUpdateString := "SELECT id,number,mac_address,type,building_name,client_name FROM machine WHERE mac_address= ?"
	row, updateErr := db.UseSQL().Query(sqlUpdateString, request.MacAddress)
	if updateErr != nil {
		//TODO dont send back sql errs
		return updateErr
	}
	hasRows := row.Next()
	if hasRows == false {
		return NotMappedError
	}
	defer func(row *sql.Rows) {
		rowCloseErr := row.Close()
		if rowCloseErr != nil {
			slog.Error("failed to close row", "error", rowCloseErr.Error())
		}
	}(row)
	var machineForCache helpers.DBMachine
	rowScanErr := row.Scan(&machineForCache.Id, &machineForCache.Number, &machineForCache.MacAddress, &machineForCache.Type, &machineForCache.Building, &machineForCache.ClientName)
	if rowScanErr != nil {
		slog.Error("failed to scan row")
	}
	machineForCache.Status = request.Status
	machineForCache.EstimatedDuration = request.TimeBetweenChange
	return updateCache(machineForCache)
}

// CacheKey builds the Redis key for a machine.
//
// The fields are sql.NullString, which has no String() method, so the previous
// "%s" formatting wrote the Go struct literal into the key:
//
//	client:{Lafayette true}:building:{Watson true}:type:{Washer true}:machine:{... true}
//
// The read path builds its pattern from plain path values, so it never matched
// and every filtered status query came back empty. Only the all-wildcard query
// worked, which is why the app appeared functional.
func CacheKey(clientName, building, machineType, macAddress string) string {
	return fmt.Sprintf("client:%s:building:%s:type:%s:machine:%s",
		clientName, building, machineType, macAddress)
}

func updateCache(machineForCache helpers.DBMachine) error {
	rds, ctx := db.UseRedis()
	key := CacheKey(
		machineForCache.ClientName.String,
		machineForCache.Building.String,
		machineForCache.Type.String,
		machineForCache.MacAddress.String,
	)
	_, redisErr := rds.Set(ctx, key, machineForCache, time.Minute*30).Result()
	if redisErr != nil {
		slog.Error("failed to write cache entry", "key", key, "error", redisErr.Error())
	}
	return redisErr
}

func updateStatus(request UpdateStatusRequest) error {
	start := time.Now()
	defer func() {
		logging.GetStatusDuration.Observe(time.Since(start).Seconds())
	}()
	sqlStart := time.Now()
	sqlUpdateString := "UPDATE machine SET status = ?,last_write = ? WHERE mac_address= ? RETURNING id,number,mac_address,type,building_name,client_name;"
	row, updateErr := db.UseSQL().Query(sqlUpdateString, request.Status, time.Now().Format(time.RFC1123), request.MacAddress)
	if updateErr != nil {
		//TODO dont send back sql errs
		return updateErr
	}
	row.Next()
	var machineForCache helpers.DBMachine
	rowScanStart := time.Now()
	rowScanErr := row.Scan(&machineForCache.Id, &machineForCache.Number, &machineForCache.MacAddress, &machineForCache.Type, &machineForCache.Building, &machineForCache.ClientName)
	if rowScanErr != nil {
		slog.Error("failed to scan row")
	}

	//
	defer func(request UpdateStatusRequest) {
		rowCloseErr := row.Close()
		addToHistory(request, machineForCache.Id)
		if rowCloseErr != nil {
			slog.Error("failed to close row", "error", rowCloseErr.Error())
		}
	}(request)
	slog.Info("sql duration", "row scan ms", time.Since(rowScanStart).Milliseconds(),
		"total ms", time.Since(sqlStart).Milliseconds())
	machineForCache.Status = request.Status
	machineForCache.EstimatedDuration = request.TimeBetweenChange
	return updateCache(machineForCache)
}

func addToHistory(body UpdateStatusRequest, machineID int) {
	//TODO turn this into a transaction
	sqlString := "INSERT INTO StatusChange (machine_id,firmware_version,Status,Confidence,Date,hour,day_of_the_week,month,year) VALUES (?,?,?,?,?,?,?,?,?)"
	_, dbErr := db.UseSQL().Exec(sqlString, machineID, body.FirmwareVersion, body.Status, body.Confidence, time.Now().Format(time.RFC1123), time.Now().Hour(), time.Now().Weekday().String(), time.Now().Month().String(), time.Now().Year())
	if dbErr != nil {
		//TODO dont send back sql errs
		slog.Error(dbErr.Error())
		return
	}
}
