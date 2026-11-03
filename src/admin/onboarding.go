package admin

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"spacesona-go-backend/auth"
	"spacesona-go-backend/db"
	"spacesona-go-backend/helpers"
	"strings"
)

type DeviceOnboardRequest struct {
	MacAddress string `json:"mac_address"`
	ClientName string `json:"client_name"`
	ClientKey  string `json:"client_key"`
}
type ClientOnboardResponse struct {
	Message string `json:"message"`
}

func BoardOnboardingRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var body DeviceOnboardRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&body)
	if decodeErr != nil {
		slog.Error("decode err", "error", decodeErr.Error())
		http.Error(w, "could not onboard device", http.StatusBadRequest)
		return
	}
	querySqlString := "SELECT name,key,salt FROM client where name = ?;"
	row, queryErr := db.UseSQL().Query(querySqlString, body.ClientName)
	if queryErr != nil {
		slog.Error("query err", "error", queryErr.Error())
		http.Error(w, "could not onboard device", http.StatusBadRequest)
		return
	}
	var client helpers.Client
	row.Next()
	rowScanErr := row.Scan(&client.Name, &client.Key, &client.Salt)
	if rowScanErr != nil {
		slog.Error("scan err", "error", rowScanErr.Error())
		http.Error(w, "could not onboard device", http.StatusBadRequest)
		return
	}
	closeErr := row.Close()
	if closeErr != nil {
		slog.Error("close err", "error", closeErr.Error())
		http.Error(w, "could not onboard device", http.StatusBadRequest)
		return
	}
	keyMatches, compareErr := auth.SecureCompareKey(client.Key.String, body.ClientKey, client.Salt)
	if compareErr != nil {
		slog.Error("client key comparison failed", "error", compareErr.Error())
		http.Error(w, "could not onboard device", http.StatusBadRequest)
		return
	}
	// The missing return here was the whole bug: the handler wrote a 400 and
	// then fell straight through to the INSERT below, so any MAC address could
	// be registered as a valid board using any key, or none.
	if !keyMatches {
		slog.Warn("onboarding attempt with wrong client key", "client", body.ClientName)
		http.Error(w, "could not onboard device", http.StatusBadRequest)
		return
	}
	newBoardSqlString := "INSERT INTO board (mac_address, valid, client_name)  VALUES (?,?,?);"
	_, execErr := db.UseSQL().Exec(newBoardSqlString, body.MacAddress, 1, client.Name)
	if execErr != nil {
		execErrString := execErr.Error()
		slog.Error("db insert err", "error", execErrString)

		if strings.Contains(execErrString, "UNIQUE constraint failed") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			response := ClientOnboardResponse{Message: "Device already onboarded!"}
			responseJson, jsonEncodeErr := json.Marshal(response)
			if jsonEncodeErr != nil {
				http.Error(w, "something went wrong! but you should already be onboarded", http.StatusInternalServerError)
			}
			_, writeErr := w.Write(responseJson)
			if writeErr != nil {
				http.Error(w, "something went wrong! but you should already be onboarded", http.StatusInternalServerError)
				return
			}
			return
		}
		http.Error(w, "failed to onboard device", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	response := ClientOnboardResponse{Message: "Successfully onboarded device!"}
	responseJson, jsonEncodeErr := json.Marshal(response)
	if jsonEncodeErr != nil {
		http.Error(w, "failed to onboard device try again later", http.StatusInternalServerError)
		return
	}
	_, writeErr := w.Write(responseJson)
	if writeErr != nil {
		http.Error(w, "failed to onboard device try again later", http.StatusInternalServerError)
		return
	}
}

type newClientRequest struct {
	ClientName string             `json:"client_name"`
	Buildings  []helpers.Building `json:"buildings"`
}
type newClientResponse struct {
	Message string `json:"message"`
	Key     string `json:"key"`
}

func ClientOnboardingRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body newClientRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&body)
	if decodeErr != nil {
		slog.Error(decodeErr.Error())
		http.Error(w, "Error With passed parameters please check the documentation or check support", http.StatusBadRequest)
		return
	}
	key, salt, keyGenErr := auth.GenKey()
	if keyGenErr != nil {
		http.Error(w, "Error generating key try again later and contact support", http.StatusBadRequest)
		return
	}
	//todo could get rid of
	newClientSqlString := "INSERT INTO client (name, key,salt) VALUES (?, ?,?);"
	_, sqlInsertClientErr := db.UseSQL().Exec(newClientSqlString, body.ClientName, key, salt)
	if sqlInsertClientErr != nil {
		slog.Error("insert client err", "error", sqlInsertClientErr.Error())
		http.Error(w, "Error creating client contact support", http.StatusBadRequest)
		return
	}
	if machinesErr := addMachinesToBuilding(body.Buildings, body.ClientName); machinesErr != nil {
		http.Error(w, "Error creating client contact support", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var response newClientResponse
	response.Message = "successfully created client"
	response.Key = key
	responseJson, jsonEncodeErr := json.Marshal(response)
	if jsonEncodeErr != nil {
		http.Error(w, "Error encoding response reload and check if correct data is in admin dashboard", http.StatusInternalServerError)
	}
	_, writeErr := w.Write(responseJson)
	if writeErr != nil {
		return
	}
}

func addMachinesToBuilding(buildings []helpers.Building, clientName string) error {
	for _, building := range buildings {
		newBuildingString := "INSERT OR IGNORE INTO building (name, client_name) VALUES (?,?);"
		// This previously passed one argument for two placeholders, so the
		// building insert always failed -- and the error was only logged.
		_, sqlInsertBuildingErr := db.UseSQL().Exec(newBuildingString, building.BuildingName, clientName)
		if sqlInsertBuildingErr != nil {
			slog.Error("failed to add building", "error", sqlInsertBuildingErr.Error())
			return fmt.Errorf("adding building %q: %w", building.BuildingName, sqlInsertBuildingErr)
		}

		if len(building.Machines) == 0 {
			continue
		}

		// Values are bound as parameters rather than formatted into the
		// statement. The previous version interpolated the client name,
		// building name and machine type directly with %s, which let any
		// caller of /onboard/client or /admin/machine/new inject SQL.
		var sb strings.Builder
		sb.WriteString("INSERT INTO machine (number,client_name,mac_address,building_name,type,status) VALUES ")
		args := make([]interface{}, 0, len(building.Machines)*5)
		for i, machine := range building.Machines {
			if i > 0 {
				sb.WriteString(",")
			}
			sb.WriteString("(?,?,NULL,?,?,0)")
			args = append(args, machine.Number, clientName, building.BuildingName, machine.Type)
		}
		sb.WriteString(";")

		_, sqlInsertMachineErr := db.UseSQL().Exec(sb.String(), args...)
		if sqlInsertMachineErr != nil {
			slog.Error("failed to add machines", "error", sqlInsertMachineErr.Error())
			return fmt.Errorf("adding machines to %q: %w", building.BuildingName, sqlInsertMachineErr)
		}
	}
	return nil
}
