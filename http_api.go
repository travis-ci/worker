package worker

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"
)

// APIHandler handles requests to worker's HTTP API.
type APIHandler struct {
	i *CLI
}

// Setup installs the HTTP routes that will handle requests to the HTTP API.
func (api *APIHandler) Setup() {
	api.i.logger.Info("setting up HTTP API")
	r := mux.NewRouter()
	r.HandleFunc("/healthz", api.HealthCheck).Methods("GET")
	r.HandleFunc("/worker", api.GetWorkerInfo).Methods("GET")
	r.HandleFunc("/worker", api.UpdateWorkerInfo).Methods("PATCH")
	r.Use(api.CheckAuth)
	http.Handle("/", r)
}

// CheckAuth is a middleware for all HTTP API methods that ensures that the
// configured basic auth credentials were passed in the request.
func (api *APIHandler) CheckAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		username, password, ok := req.BasicAuth()
		if !ok {
			w.Header().Set("WWW-Authenticate", "Basic realm=\"travis-ci/worker\"")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		authBytes := []byte(fmt.Sprintf("%s:%s", username, password))
		if subtle.ConstantTimeCompare(authBytes, []byte(api.i.c.String("http-api-auth"))) != 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		// pass it on!
		next.ServeHTTP(w, req)
	})
}

// HealthCheck indicates whether worker is currently functioning in a healthy
// way. This can be used by a system like Kubernetes to determine whether to
// replace an instance of worker with a new one.
func (api *APIHandler) HealthCheck(w http.ResponseWriter, req *http.Request) {
	// TODO actually check that processors are running and ready
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "OK")
}

// GetWorkerInfo writes a JSON payload with useful information about the current
// state of worker as a whole.
func (api *APIHandler) GetWorkerInfo(w http.ResponseWriter, req *http.Request) {
	info := api.i.workerInfo()

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(info)
}

// UpdateWorkerInfo allows reconfiguring some parts of worker on the fly.
//
// The main use of this is adjusting the size of the processor pool without
// interrupting existing running jobs.
func (api *APIHandler) UpdateWorkerInfo(w http.ResponseWriter, req *http.Request) {
	var info workerInfo
	if err := json.NewDecoder(req.Body).Decode(&info); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errorResponse{
			Message: err.Error(),
		})
		return
	}

	if info.PoolSize > 0 {
		api.i.ProcessorPool.SetSize(info.PoolSize)
	}

	w.WriteHeader(http.StatusNoContent)
}

type workerInfo struct {
	Version          string `json:"version"`
	Revision         string `json:"revision"`
	PoolSize         int    `json:"poolSize"`
	ExpectedPoolSize int    `json:"expectedPoolSize"`
}

func (i *CLI) workerInfo() workerInfo {
	return workerInfo{
		Version:          VersionString,
		Revision:         RevisionString,
		PoolSize:         i.ProcessorPool.Size(),
		ExpectedPoolSize: i.ProcessorPool.ExpectedSize(),
	}
}

type errorResponse struct {
	Message string `json:"error"`
}
