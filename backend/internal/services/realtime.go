package services

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"cascata-backend/internal/types"
	"github.com/jackc/pgx/v5"
)

type RealtimeClient struct {
	ID          string
	Writer      http.ResponseWriter
	Flusher     http.Flusher
	TableFilter string
	Env         string
	Done        chan bool
}

type ProjectListener struct {
	Ctx    context.Context
	Cancel context.CancelFunc
	Refs   int
}

var (
	subscribers     sync.Map // [string]map[string]*RealtimeClient (key: slug:env)
	activeListeners sync.Map // [string]*ProjectListener (key: slug:env)
	listenerMutex   sync.Mutex
)

// HandleRealtimeConnection upgrades an HTTP request to SSE
func HandleRealtimeConnection(w http.ResponseWriter, r *http.Request, project *types.Project, env string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	clientID := fmt.Sprintf("%d", time.Now().UnixNano())
	client := &RealtimeClient{
		ID:      clientID,
		Writer:  w,
		Flusher: flusher,
		Env:     env,
		Done:    make(chan bool),
	}

	contextKey := project.Slug + ":" + env
	
	// Register Client
	rawClients, _ := subscribers.LoadOrStore(contextKey, make(map[string]*RealtimeClient))
	clients := rawClients.(map[string]*RealtimeClient)
	clients[clientID] = client
	subscribers.Store(contextKey, clients)

	// Ensure Listener - pass project.DbName to ensure correct database connection
	go startProjectListener(project.Slug, env, project.DbName)

	// Send Connected Message (TypeScript Parity)
	connectMsg := fmt.Sprintf(`{"type":"connected","clientId":"%s","env":"%s"}`, clientID, env)
	fmt.Fprintf(w, "data: %s\n\n", connectMsg)
	flusher.Flush()

	// Keep alive / Wait for disconnect
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			// Disconnect
			rawClients, _ := subscribers.Load(contextKey)
			if rawClients != nil {
				clients := rawClients.(map[string]*RealtimeClient)
				delete(clients, clientID)
				subscribers.Store(contextKey, clients)
			}
			return
		}
	}
}

func startProjectListener(projectSlug, env string, dbName string) {
	listenerMutex.Lock()
	contextKey := projectSlug + ":" + env
	if _, ok := activeListeners.Load(contextKey); ok {
		listenerMutex.Unlock()
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	activeListeners.Store(contextKey, &ProjectListener{Ctx: ctx, Cancel: cancel, Refs: 1})
	listenerMutex.Unlock()

	defer activeListeners.Delete(contextKey)

	// Use db_name from project (e.g., "cascata_teste") not raw slug
	// This ensures we connect to the correct database name
	actualDbName := dbName
	
	connStr := getTenantConnectionString(actualDbName)
	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		log.Printf("[Realtime] Listener failed to connect to %s (db=%s): %v", contextKey, actualDbName, err)
		return
	}
	defer conn.Close(ctx)

	_, err = conn.Exec(ctx, "LISTEN cascata_events")
	if err != nil {
		log.Printf("[Realtime] LISTEN command failed for %s: %v", contextKey, err)
		return
	}

	log.Printf("[Realtime] 🟢 Started LISTEN for %s", contextKey)

	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			log.Printf("[Realtime] Notification error for %s: %v", contextKey, err)
			return
		}

		broadcast(contextKey, notification.Payload)
	}
}

func broadcast(contextKey, payload string) {
	rawClients, ok := subscribers.Load(contextKey)
	if !ok { return }

	clients := rawClients.(map[string]*RealtimeClient)
	msg := fmt.Sprintf("data: %s\n\n", payload)

	for id, client := range clients {
		_, err := fmt.Fprint(client.Writer, msg)
		if err != nil {
			// Client disconnected, remove from map
			delete(clients, id)
			continue
		}
		client.Flusher.Flush()
	}
	// Update the subscribers map with cleaned clients
	subscribers.Store(contextKey, clients)
}
