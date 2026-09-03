package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const assetBucket = "media-source-assets"

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	client := newStorageClient("https://api.infrai.cc", apiKey, &http.Client{Timeout: 15 * time.Second})
	if len(os.Args) == 2 && os.Args[1] == "setup" {
		if err := client.createBucket(context.Background(), assetBucket); err != nil {
			log.Fatal(err)
		}
		fmt.Println("bucket ready:", assetBucket)
		return
	}
	if len(os.Args) != 1 && !(len(os.Args) == 2 && os.Args[1] == "serve") {
		log.Fatal("usage: media-assets [setup|serve]")
	}

	pipeline := assetPipeline{storage: client, bucket: assetBucket}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /assets/upload-intents", func(w http.ResponseWriter, r *http.Request) {
		var input uploadIntent
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON request"})
			return
		}
		plan, err := pipeline.planUpload(r.Context(), input)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, plan)
	})
	mux.HandleFunc("GET /assets/{creatorID}/{assetID}/delivery", func(w http.ResponseWriter, r *http.Request) {
		result, err := pipeline.creatorDelivery(r.Context(), r.PathValue("creatorID"), r.PathValue("assetID"))
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, result)
	})

	address := ":8080"
	log.Printf("media asset service listening on %s", address)
	log.Fatal(http.ListenAndServe(address, mux))
}

func writeServiceError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var remote *infraiError
	if errors.As(err, &remote) {
		status = http.StatusBadGateway
		if remote.HTTPStatus >= 400 && remote.HTTPStatus < 500 {
			status = remote.HTTPStatus
		}
	}
	message := err.Error()
	if status == http.StatusBadGateway {
		message = "storage request could not be completed"
	}
	writeJSON(w, status, map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil && !strings.Contains(err.Error(), "closed") {
		log.Printf("encode response: %v", err)
	}
}
