package worker

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

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
	r.HandleFunc("/worker", api.ShutdownWorker).Methods("DELETE")

	// It is preferable to use UpdateWorkerInfo to update the pool size,
	// as it does not depend on the current state of worker.
	r.HandleFunc("/pool/increment", api.IncrementPool).Methods("POST")
	r.HandleFunc("/pool/decrement", api.DecrementPool).Methods("POST")

	r.Use(api.CheckAuth)
	http.Handle("/", r)
}

// CheckAuth is a middleware for all HTTP API methods that ensures that the
// configured basic auth credentials were passed in the request.
func (api *APIHandler) CheckAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// skip auth for the health check endpoint
		if strings.HasPrefix(req.URL.Path, "/healthz") {
			next.ServeHTTP(w, req)
			return
		}

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

	w.Header().Set("Content-Type", "application/json")
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

// ShutdownWorker tells the worker to shutdown.
//
// Options can be passed in the body that determine whether the shutdown is
// done gracefully or not.
func (api *APIHandler) ShutdownWorker(w http.ResponseWriter, req *http.Request) {
	var options shutdownOptions
	if err := json.NewDecoder(req.Body).Decode(&options); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(errorResponse{
			Message: err.Error(),
		})
		return
	}

	if options.Graceful {
		api.i.ProcessorPool.GracefulShutdown(options.Pause)
	} else {
		api.i.cancel()
	}
	w.WriteHeader(http.StatusNoContent)
}

// IncrementPool tells the worker to spin up another processor.
func (api *APIHandler) IncrementPool(w http.ResponseWriter, req *http.Request) {
	api.i.ProcessorPool.Incr()
	w.WriteHeader(http.StatusNoContent)
}

// DecrementPool tells the worker to gracefully shutdown a processor.
func (api *APIHandler) DecrementPool(w http.ResponseWriter, req *http.Request) {
	api.i.ProcessorPool.Decr()
	w.WriteHeader(http.StatusNoContent)
}

type workerInfo struct {
	Version          string `json:"version"`
	Revision         string `json:"revision"`
	Generated        string `json:"generated"`
	Uptime           string `json:"uptime"`
	PoolSize         int    `json:"poolSize"`
	ExpectedPoolSize int    `json:"expectedPoolSize"`
	TotalProcessed   int    `json:"totalProcessed"`

	Processors []processorInfo `json:"processors"`
}

func (i *CLI) workerInfo() workerInfo {
	info := workerInfo{
		Version:          VersionString,
		Revision:         RevisionString,
		Generated:        GeneratedString,
		Uptime:           time.Since(i.bootTime).String(),
		PoolSize:         i.ProcessorPool.Size(),
		ExpectedPoolSize: i.ProcessorPool.ExpectedSize(),
		TotalProcessed:   i.ProcessorPool.TotalProcessed(),
	}

	i.ProcessorPool.Each(func(_ int, p *Processor) {
		info.Processors = append(info.Processors, p.processorInfo())
	})

	return info
}

type processorInfo struct {
	ID        string `json:"id"`
	Processed int    `json:"processed"`
	Status    string `json:"status"`
	LastJobID uint64 `json:"lastJobId"`
}

func (p *Processor) processorInfo() processorInfo {
	return processorInfo{
		ID:        p.ID,
		Processed: p.ProcessedCount,
		Status:    p.CurrentStatus,
		LastJobID: p.LastJobID,
	}
}

type shutdownOptions struct {
	Graceful bool `json:"graceful"`
	Pause    bool `json:"pause"`
}

type errorResponse struct {
	Message string `json:"error"`
}
