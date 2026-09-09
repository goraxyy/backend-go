package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

func FileRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	fileString := r.PathValue("version") + ".bin"
	filePath := filepath.Join("./firmware", fileString)
	fmt.Println(filePath)
	dat, fileReadErr := os.ReadFile(filePath)
	if fileReadErr != nil {
		fmt.Println(fileReadErr)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		jsonErr := json.NewEncoder(w).Encode("bad request")
		if jsonErr != nil {
			return
		}
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(dat)))
	_, writeErr := w.Write(dat)
	if writeErr != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		jsonErr := json.NewEncoder(w).Encode("bad request")
		if jsonErr != nil {
			return
		}
		return
	}
}

type latestVersionResponse struct {
	Version string `json:"version"`
}

func LatestVersionRoute(w http.ResponseWriter, r *http.Request) {
	versions, getVersionErr := getAllVersions("./firmware")
	if getVersionErr != nil {
		http.Error(w, getVersionErr.Error(), 400)
		return
	}
	if r.Method != http.MethodGet {
		http.NotFound(w, r)
		return
	}
	response := latestVersionResponse{Version: versions[0]}
	versionsString, encodeErr := json.Marshal(response)
	if encodeErr != nil {
		http.Error(w, encodeErr.Error(), 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, writeErr := w.Write(versionsString)
	if writeErr != nil {
		return
	}
}

type versionNumbers []string

// todo sort this
func getAllVersions(path string) ([]string, error) {
	firmwareVersions := make([]string, 0)
	firmwareFiles, dirErr := os.ReadDir(path)
	if dirErr != nil {
		return nil, dirErr
	}
	for _, f := range firmwareFiles {
		if f.IsDir() {
			recursiveVersions, recursiveErr := getAllVersions(filepath.Join(path, f.Name()))
			if recursiveErr != nil {
				return nil, recursiveErr
			}
			firmwareVersions = append(firmwareVersions, recursiveVersions...)
		} else {
			firmwareVersions = append(firmwareVersions, strings.Split(f.Name(), ".")[0])
		}
	}
	slices.SortFunc(firmwareVersions, compareVersions) // in place sorting
	slices.Reverse(firmwareVersions)
	return firmwareVersions, nil
}

func compareVersions(versionA string, versionB string) int {
	if versionA == versionB {
		return 0
	}
	splitA := strings.Split(versionA, "-")
	splitB := strings.Split(versionB, "-")
	if splitA[0] > splitB[0] { //A major version is bigger
		return 1
	}
	if splitA[1] > splitB[1] { //A minor version is bigger
		return 1
	}
	if splitA[2] > splitB[2] { //A patch version is bigger
		return 1
	}
	//If we hit this then A was never bigger so B is bigger
	return -1
}
