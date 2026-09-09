package admin

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"spacesona-go-backend/db"
	"spacesona-go-backend/helpers"
)

type BoardAssignmentRequest struct {
	Mappings []BoardMapping `json:"mappings"`
}

type BoardMapping struct {
	MacAddress string `json:"mac_address"`
	MachineId  int    `json:"machine_id"`
}

type AssignBoardResponse struct { // todo consolidate to spacesona responce with better stuffs
	Message string
}

func AssignBoardEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		slog.Error("AssignBoardEndpoint expects POST", "method", r.Method)
		http.NotFound(w, r)
		return
	}
	var body BoardAssignmentRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&body)
	if decodeErr != nil {
		slog.Error(decodeErr.Error())
		http.Error(w, "Could not assign machine", http.StatusBadRequest)
		return
	}
	for _, mapping := range body.Mappings {
		sqlUpdateString := "UPDATE machine SET mac_address = ? WHERE id = ?;"
		_, updateErr := db.UseSQL().Exec(sqlUpdateString, mapping.MacAddress, mapping.MachineId)
		if updateErr != nil {
			slog.Error(updateErr.Error())
			http.Error(w, "Could not assign machine", http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var response AssignBoardResponse
	response.Message = "successfully assigned machine's"
	responseJson, jsonEncodeErr := json.Marshal(response)
	if jsonEncodeErr != nil {
		http.Error(w, "Error encoding response reload and check if correct data is in admin dashboard", http.StatusInternalServerError)
	}
	_, writeErr := w.Write(responseJson)
	if writeErr != nil {
		return
	}
}

type boardValidStatusRequest struct {
	Mappings []BoardStatusMapping `json:"board_status_mappings"`
}
type BoardStatusMapping struct {
	MacAddress string `json:"mac_address"`
	Valid      bool   `json:"valid"`
}

func SetBoardValid(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body boardValidStatusRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&body)
	if decodeErr != nil {
		slog.Error(decodeErr.Error())
		http.Error(w, "Could not change board valid status", http.StatusBadRequest)
		return
	}
	for _, mapping := range body.Mappings {
		sqlUpdateString := "UPDATE board SET valid = ? WHERE mac_address = ?"
		_, updateErr := db.UseSQL().Exec(sqlUpdateString, mapping.Valid, mapping.MacAddress)
		if updateErr != nil {
			slog.Error(updateErr.Error())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var response AssignBoardResponse
	response.Message = "changed boards valid state"
	responseJson, jsonEncodeErr := json.Marshal(response)
	if jsonEncodeErr != nil {
		http.Error(w, "Error encoding response reload and check if correct data is in admin dashboard", http.StatusInternalServerError)
	}
	_, writeErr := w.Write(responseJson)
	if writeErr != nil {
		return
	}
}

type AddNewMachinesRequest struct {
	ClientName string             `json:"client_name"`
	Buildings  []helpers.Building `json:"buildings"`
}

func AddNewMachines(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body AddNewMachinesRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&body)
	if decodeErr != nil {
		http.Error(w, "Error With passed parameters please check the documentation or check support", http.StatusBadRequest)
		return
	}
	if machinesErr := addMachinesToBuilding(body.Buildings, body.ClientName); machinesErr != nil {
		http.Error(w, "Could not add machines", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	var response AssignBoardResponse
	response.Message = "successfully added machines client"
	responseJson, jsonEncodeErr := json.Marshal(response)
	if jsonEncodeErr != nil {
		http.Error(w, "Error encoding response reload and check if correct data is in admin dashboard", http.StatusInternalServerError)
	}
	_, writeErr := w.Write(responseJson)
	if writeErr != nil {
		return
	}
}
