package status

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"spacesona-go-backend/db"
	"spacesona-go-backend/helpers"
	"spacesona-go-backend/logging"
	"strings"
	"time"
)

type GetStatusResponse struct {
	Statuses map[int]publicMachine
}

// publicMachine is what /status/... returns to anonymous callers.
//
// It deliberately omits mac_address. This endpoint is unauthenticated and is
// polled by the student web app, so it was publishing the MAC of every board
// in the fleet -- and a MAC address was the only thing /auth/device required
// to mint a 24-hour token. Field names and types otherwise match DBMachine so
// existing clients keep working unchanged.
type publicMachine struct {
	Id                int            `json:"id"`
	Number            int            `json:"number"`
	Type              sql.NullString `json:"type"`
	Status            bool           `json:"status"`
	Building          sql.NullString `json:"building"`
	ClientName        sql.NullString `json:"client_name"`
	EstimatedDuration int            `json:"estimated_duration"`
	NumberOfRuns      int            `json:"number_of_runs"`
}

func toPublicMachine(m helpers.DBMachine) publicMachine {
	return publicMachine{
		Id:                m.Id,
		Number:            m.Number,
		Type:              m.Type,
		Status:            m.Status,
		Building:          m.Building,
		ClientName:        m.ClientName,
		EstimatedDuration: m.EstimatedDuration,
		NumberOfRuns:      m.NumberOfRuns,
	}
}

// statusPathSegments resolves the query scope from the request path.
//
// Go's ServeMux wildcards match a single non-empty segment, and a pattern that
// does not end in "/" will not match a path that does. So "/status/lafTest/"
// misses "/status/{client}" and falls through to the bare "/status/" subtree
// route, where PathValue("client") is "" and every filter widens to "*".
//
// The student app requests exactly that URL, so a request that looks scoped to
// one customer was silently returning every customer's machines. Deriving the
// segments from the path directly makes the trailing slash irrelevant.
func statusPathSegments(r *http.Request) (client, building, machineType, macAddress string) {
	// PathValue still wins when the mux populated it.
	client = r.PathValue("client")
	building = r.PathValue("building")
	machineType = r.PathValue("type")
	macAddress = r.PathValue("machineId")

	if client != "" {
		return client, building, machineType, macAddress
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/status"), "/")
	if rest == "" {
		return "", "", "", ""
	}
	segments := strings.Split(rest, "/")
	get := func(i int) string {
		if i < len(segments) {
			return segments[i]
		}
		return ""
	}
	return get(0), get(1), get(2), get(3)
}

func GetStatusRoute(w http.ResponseWriter, r *http.Request) {
	clientName, buildingName, machineType, macAddress := statusPathSegments(r)
	start := time.Now()
	defer func() {
		logging.GetStatusDuration.Observe(time.Since(start).Seconds())
	}()
	rds, ctx := db.UseRedis()
	//SCAN 0 MATCH "client:Lafayette:building:Watson Hall:*"
	if clientName == "" {
		clientName = "*"
	}
	if buildingName == "" {
		buildingName = "*"
	}
	if machineType == "" {
		machineType = "*"
	}
	if macAddress == "" {
		macAddress = "*"
	}
	//TODO this could be slow and may need optimisation in the future but on the upside no db reads
	// The last argument was machineType, not macAddress -- the computed
	// macAddress was never used. Both sides now go through CacheKey so the
	// write and read formats cannot drift apart again.
	rdsScanQuery := CacheKey(clientName, buildingName, machineType, macAddress)
	machines, _, rdsErr := rds.Scan(ctx, 0, rdsScanQuery, 100).Result()
	if rdsErr != nil {
		slog.Error(rdsErr.Error())
		return
	}
	//todo Also could be slow depending on the query but for now its like only ~150 machines so its fine :)
	machinesFromeCache := make(map[int]publicMachine)
	for _, machine := range machines {
		result, GetValueErr := rds.Get(ctx, machine).Result()
		if GetValueErr != nil {
			slog.Error(GetValueErr.Error())
		}
		var decoded helpers.DBMachine
		decodeErr := json.Unmarshal([]byte(result), &decoded)
		if decodeErr != nil {
			slog.Error(decodeErr.Error())
		}
		machinesFromeCache[decoded.Id] = toPublicMachine(decoded)
	}
	getStatusResponse := GetStatusResponse{machinesFromeCache}
	responseString, jsonEncodeErr := json.Marshal(getStatusResponse)
	if jsonEncodeErr != nil {
		http.Error(w, jsonEncodeErr.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, writeErr := w.Write(responseString)
	if writeErr != nil {
		http.Error(w, writeErr.Error(), http.StatusInternalServerError)
		return
	}
	return
}

///-------------------- old code

type boardStatus struct {
	MacAddress   string
	Status       bool
	MachineId    int
	MachineNum   int
	BuildingName string
	ClientName   string
	MachineType  string
}

func getFromDb(clientName string, buildingName string, machineType string, machineId string) ([]boardStatus, error) {

	baseQueryString := "SELECT id,number,mac_address, type, building_name, machine.client_name,status FROM machine %s ;"
	var allStatuses []boardStatus
	var getStatusError error
	if clientName == "" && buildingName == "" && machineType == "" && machineId == "" {
		queryString := fmt.Sprintf(baseQueryString, "")
		rows, queryErr := db.UseSQL().Query(queryString, clientName)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
	}
	if clientName != "" && buildingName == "" && machineType == "" && machineId == "" {
		queryString := fmt.Sprintf(baseQueryString, "WHERE machine.client_name = ?")
		rows, queryErr := db.UseSQL().Query(queryString, clientName)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
	}
	if clientName != "" && buildingName != "" && machineType == "" && machineId == "" {
		// return all for client in building
		queryString := fmt.Sprintf(baseQueryString, "WHERE machine.client_name = ? AND building_name = ? ")
		rows, queryErr := db.UseSQL().Query(queryString, clientName, buildingName)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
	}
	if clientName != "" && buildingName != "" && machineType != "" && machineId == "" {
		queryString := fmt.Sprintf(baseQueryString, "WHERE machine.client_name = ? AND building_name = ? AND type = ?")
		rows, queryErr := db.UseSQL().Query(queryString, clientName, buildingName, machineType)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
		// return all for client in building with type
	}
	if clientName != "" && buildingName != "" && machineType != "" && machineId != "" {
		queryString := fmt.Sprintf(baseQueryString, "WHERE machine.id = ?")
		rows, queryErr := db.UseSQL().Query(queryString, machineId)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
		// return machine Status
	}
	return allStatuses, getStatusError
}

func GetStatusHistory(w http.ResponseWriter, r *http.Request) ([]boardStatus, error) {

	clientName := r.PathValue("client")
	buildingName := r.PathValue("building")
	machineType := r.PathValue("type")
	machineId := r.PathValue("machineId")
	baseQueryString := `with Status as (
select
    StatusChange.id,
    StatusChange.mac_address,
    StatusChange.current_version,
    StatusChange.Status,
    StatusChange.Confidence,
    StatusChange.date,
    row_number() over (partition by StatusChange.mac_address order by StatusChange.Date desc) as RN
	FROM StatusChange
	) select Status.id,
         Status.mac_address,
         Status.current_version,
         Status.Status,
         Status.Confidence,
         Status.Date,
         machine.id as machine_id,
         machine.number as machine_num,
         machine.building_name as building_name,
         machine.client_name as client_name,
         machine.type as machine_type
         from Status
                          inner join main.board board on board.mac_address = Status.mac_address
                          inner join main.machine machine on board.mac_address = machine.mac_address
	where RN = 1 %s order by Status.mac_address;`
	fmt.Println(clientName, buildingName, machineType, machineId)
	var allStatuses []boardStatus
	var getStatusError error
	if clientName == "" && buildingName == "" && machineType == "" && machineId == "" {
		queryString := fmt.Sprintf(baseQueryString, "")
		rows, queryErr := db.UseSQL().Query(queryString, clientName)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
	}
	if clientName != "" && buildingName == "" && machineType == "" && machineId == "" {
		queryString := fmt.Sprintf(baseQueryString, "AND client_name = ?")
		rows, queryErr := db.UseSQL().Query(queryString, clientName)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
	}
	if clientName != "" && buildingName != "" && machineType == "" && machineId == "" {
		// return all for client in building
		queryString := fmt.Sprintf(baseQueryString, "AND client_name = ? and building_name = ? ")
		rows, queryErr := db.UseSQL().Query(queryString, clientName, buildingName)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
	}
	if clientName != "" && buildingName != "" && machineType != "" && machineId == "" {
		queryString := fmt.Sprintf(baseQueryString, "AND client_name = ? and building_name = ? and type = ?")
		rows, queryErr := db.UseSQL().Query(queryString, clientName, buildingName, machineType)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
		// return all for client in building with type
	}
	if clientName != "" && buildingName != "" && machineType != "" && machineId != "" {
		queryString := fmt.Sprintf(baseQueryString, "AND machine.id = ?")
		rows, queryErr := db.UseSQL().Query(queryString, machineId)
		allStatuses, getStatusError = GetStatusFromDb(rows, queryErr)
		// return machine Status
	}
	if getStatusError != nil {
		http.Error(w, getStatusError.Error(), http.StatusInternalServerError)
		return nil, getStatusError
	}
	return allStatuses, getStatusError
}

func GetStatusFromDb(rows *sql.Rows, rowErr error) ([]boardStatus, error) {
	allStatus := make([]boardStatus, 0)
	if rowErr != nil {
		return nil, rowErr
	}
	defer func(rows *sql.Rows) { // close rows at end of func
		rowCloseErr := rows.Close()
		if rowCloseErr != nil {
			fmt.Println("Error closing rows")
		}
	}(rows)

	for rows.Next() {
		var status boardStatus
		rowParseErr := rows.Scan(&status.MachineId, &status.MachineNum, &status.MacAddress, &status.MachineType, &status.BuildingName, &status.ClientName, &status.Status)
		if rowParseErr != nil {
			fmt.Println("failed to parse row", rowParseErr)
		}
		allStatus = append(allStatus, status)
	}

	return allStatus, nil
}
