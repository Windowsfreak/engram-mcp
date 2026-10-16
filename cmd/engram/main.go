package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/windowsfreak/engram-mcp/internal/chunker"
	"github.com/windowsfreak/engram-mcp/internal/config"
	"github.com/windowsfreak/engram-mcp/internal/db"
	"github.com/windowsfreak/engram-mcp/internal/embedder"
	"github.com/windowsfreak/engram-mcp/internal/mcp"
	"github.com/windowsfreak/engram-mcp/internal/search"
	"github.com/windowsfreak/engram-mcp/internal/vfs"
)

func main() {
	configPath := flag.String("config", "", "Path to engram config.yaml file")
	workspaceFlag := flag.String("workspace", "", "Override target workspace ID")
	flag.Parse()

	args := flag.Args()
	command := "serve-stdio"
	if len(args) > 0 {
		command = args[0]
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	activeWorkspace := cfg.Storage.DefaultWorkspaceID
	if *workspaceFlag != "" {
		activeWorkspace = *workspaceFlag
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	switch command {
	case "version":
		fmt.Println("Engram-MCP v0.1.0 (Filesystem-First Hybrid Memory Engine in Go)")

	case "migrate":
		database, err := initDB(ctx, cfg)
		if err != nil {
			log.Fatalf("Migration failed: %v", err)
		}
		defer database.Close()
		fmt.Println("Database migrations applied successfully.")

	case "reindex":
		database, err := initDB(ctx, cfg)
		if err != nil {
			log.Fatalf("Database connection failed: %v", err)
		}
		defer database.Close()

		svc, _, err := initServices(ctx, cfg, database)
		if err != nil {
			log.Fatalf("Initialization failed: %v", err)
		}

		v, err := svc.GetVFS(activeWorkspace)
		if err != nil {
			log.Fatalf("Failed getting VFS: %v", err)
		}

		reconciler := vfs.NewReconciler(v, database, func(ctx context.Context, wsID, relPath string) error {
			data, err := v.ReadFile(relPath)
			if err != nil {
				return err
			}
			_, err = svc.Write(ctx, wsID, relPath, string(data), true)
			return err
		})

		fmt.Printf("Reconciling workspace %q...\n", activeWorkspace)
		res, err := reconciler.Reconcile(ctx)
		if err != nil {
			log.Fatalf("Reconciliation failed: %v", err)
		}
		fmt.Printf("Reconciliation complete: %d created, %d updated, %d deleted, %d unchanged\n",
			res.CreatedCount, res.UpdatedCount, res.DeletedCount, res.UnchangedCount)

	case "serve-stdio":
		database, _ := initDB(ctx, cfg)
		if database != nil {
			defer database.Close()
		}

		svc, handler, err := initServices(ctx, cfg, database)
		if err != nil {
			log.Fatalf("Failed initializing services: %v", err)
		}

		// Start VFS Watcher on active workspace
		startWatcher(ctx, svc, activeWorkspace)

		server := mcp.NewStdioServer(handler, activeWorkspace)
		if err := server.Serve(ctx); err != nil {
			log.Fatalf("Stdio server terminated: %v", err)
		}

	case "serve-http":
		database, err := initDB(ctx, cfg)
		if err != nil {
			log.Fatalf("Database connection failed: %v", err)
		}
		defer database.Close()

		svc, handler, err := initServices(ctx, cfg, database)
		if err != nil {
			log.Fatalf("Failed initializing services: %v", err)
		}

		// Start VFS Watchers on all configured workspaces
		for wsID := range cfg.Storage.Workspaces {
			startWatcher(ctx, svc, wsID)
		}

		httpServer := mcp.NewHTTPServer(cfg, handler)
		go func() {
			<-ctx.Done()
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer shutdownCancel()
			_ = httpServer.Shutdown(shutdownCtx)
		}()

		log.Printf("Starting Engram-MCP HTTP server on %s...", cfg.Server.HTTPAddr)
		if err := httpServer.Start(); err != nil && err != os.ErrProcessDone {
			log.Fatalf("HTTP server failed: %v", err)
		}

	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\nAvailable commands: serve-stdio, serve-http, reindex, migrate, version\n", command)
		os.Exit(1)
	}
}

func initDB(ctx context.Context, cfg *config.Config) (*db.DB, error) {
	database, err := db.New(ctx, cfg.Database.URL, cfg.Database.MinConnections, cfg.Database.MaxConnections)
	if err != nil {
		return nil, fmt.Errorf("failed connecting to postgres: %w", err)
	}

	if err := database.RunMigrations(ctx); err != nil {
		database.Close()
		return nil, fmt.Errorf("failed running migrations: %w", err)
	}

	return database, nil
}

func initServices(ctx context.Context, cfg *config.Config, database *db.DB) (*mcp.Service, *mcp.Handler, error) {
	var emb embedder.Embedder
	if cfg.Embedder.Provider == "openai" {
		emb = embedder.NewOpenAIEmbedder(cfg.Embedder.OpenAI)
	} else {
		emb = embedder.NewOllamaEmbedder(cfg.Embedder.Ollama)
	}

	var workerPool *embedder.WorkerPool
	if database != nil {
		workerPool = embedder.NewWorkerPool(database, emb, 16, 2*time.Second)
		workerPool.Start(ctx)
	}

	searchEngine := search.NewEngine(database, emb, &cfg.Search)
	chk := chunker.NewChunker(cfg.Embedder.Provider)

	svc := mcp.NewService(cfg, database, searchEngine, chk, workerPool)
	handler := mcp.NewHandler(svc)

	return svc, handler, nil
}

func startWatcher(ctx context.Context, svc *mcp.Service, workspaceID string) {
	v, err := svc.GetVFS(workspaceID)
	if err != nil {
		return
	}

	watcher, err := vfs.NewWatcher(v, 400*time.Millisecond, func(event vfs.FileEvent) {
		switch event.Type {
		case vfs.EventCreate, vfs.EventModify:
			data, err := v.ReadFile(event.Path)
			if err == nil {
				_, _ = svc.Write(ctx, event.WorkspaceID, event.Path, string(data), true)
			}
		case vfs.EventDelete:
			_, _ = svc.MoveOrDelete(ctx, event.WorkspaceID, "delete", event.Path, "")
		}
	})
	if err == nil {
		_ = watcher.Start(ctx)
	}
}
