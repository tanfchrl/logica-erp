// Command api runs the Logica ERP HTTP server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/tandigital/logica-erp/internal/accounting/account"
	"github.com/tandigital/logica-erp/internal/agentcontract"
	"github.com/tandigital/logica-erp/internal/accounting/company"
	"github.com/tandigital/logica-erp/internal/accounting/customer"
	"github.com/tandigital/logica-erp/internal/accounting/item"
	"github.com/tandigital/logica-erp/internal/accounting/journalentry"
	"github.com/tandigital/logica-erp/internal/accounting/efaktur"
	"github.com/tandigital/logica-erp/internal/accounting/fiscalyear"
	"github.com/tandigital/logica-erp/internal/accounting/paymententry"
	"github.com/tandigital/logica-erp/internal/accounting/periodclosing"
	"github.com/tandigital/logica-erp/internal/accounting/purchaseinvoice"
	"github.com/tandigital/logica-erp/internal/accounting/reports"
	"github.com/tandigital/logica-erp/internal/accounting/salesinvoice"
	"github.com/tandigital/logica-erp/internal/accounting/supplier"
	"github.com/tandigital/logica-erp/internal/accounting/taxtemplate"
	"github.com/tandigital/logica-erp/internal/assets/asset"
	"github.com/tandigital/logica-erp/internal/crm/lead"
	"github.com/tandigital/logica-erp/internal/hr/employee"
	hrpayroll "github.com/tandigital/logica-erp/internal/hr/payroll"
	"github.com/tandigital/logica-erp/internal/manufacturing/bom"
	"github.com/tandigital/logica-erp/internal/manufacturing/workorder"
	"github.com/tandigital/logica-erp/internal/platform/crosscut"
	platformprint "github.com/tandigital/logica-erp/internal/platform/print"
	"github.com/tandigital/logica-erp/internal/pos"
	"github.com/tandigital/logica-erp/internal/projects/project"
	"github.com/tandigital/logica-erp/internal/stock/stockentry"
	"github.com/tandigital/logica-erp/internal/stock/warehouse"
	"github.com/tandigital/logica-erp/internal/support/issue"
	"github.com/tandigital/logica-erp/internal/config"
	"github.com/tandigital/logica-erp/internal/platform/auth"
	"github.com/tandigital/logica-erp/internal/platform/apitokens"
	"github.com/tandigital/logica-erp/internal/platform/audit"
	"github.com/tandigital/logica-erp/internal/platform/connectors"
	"github.com/tandigital/logica-erp/internal/platform/dataimport"
	"github.com/tandigital/logica-erp/internal/platform/notifrules"
	"github.com/tandigital/logica-erp/internal/platform/payrollconfig"
	"github.com/tandigital/logica-erp/internal/platform/sysinsights"
	"github.com/tandigital/logica-erp/internal/platform/dbx"
	"github.com/tandigital/logica-erp/internal/platform/email"
	"github.com/tandigital/logica-erp/internal/platform/httpx"
	"github.com/tandigital/logica-erp/internal/platform/identity"
	"github.com/tandigital/logica-erp/internal/platform/naming"
	"github.com/tandigital/logica-erp/internal/platform/permission"
	"github.com/tandigital/logica-erp/internal/platform/webhooks"
	"github.com/tandigital/logica-erp/internal/platform/workflow"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config", "err", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := dbx.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		logger.Error("db", "err", err)
		os.Exit(2)
	}
	defer db.Close()

	signer := auth.NewSigner(cfg.JWTSecret, cfg.AccessTokenTTL)
	perm := permission.NewEngine(db)

	companySvc := company.NewService(db)
	accountSvc := account.NewService(db)
	jeSvc := journalentry.NewService(db)
	customerSvc := customer.NewService(db)
	supplierSvc := supplier.NewService(db)
	itemSvc := item.NewService(db)
	taxSvc := taxtemplate.NewService(db)
	siSvc := salesinvoice.NewService(db)
	piSvc := purchaseinvoice.NewService(db)
	peSvc := paymententry.NewService(db)
	reportSvc := reports.NewService(db)
	pcvSvc := periodclosing.NewService(db)
	whSvc := warehouse.NewService(db)
	steSvc := stockentry.NewService(db)
	leadSvc := lead.NewService(db)
	projSvc := project.NewService(db)
	bomSvc := bom.NewService(db)
	woSvc := workorder.NewService(db)
	assetSvc := asset.NewService(db)
	empSvc := employee.NewService(db)
	payrollSvc := hrpayroll.NewService(db)
	posSvc := pos.NewService(db)
	issueSvc := issue.NewService(db)
	efakturSvc := efaktur.NewService(db)
	crosscutSvc := crosscut.NewService(db)
	namingAdminSvc := naming.NewAdminService(db)
	emailSvc := email.NewService(db)
	auditQuerySvc := audit.NewQueryService(db)
	auditViewRec  := audit.NewViewRecorder(db)
	timelineSvc   := audit.NewTimelineService(db)

	// Agent contract registry: scans /internal/<module>/AGENT_CONTRACT.md
	// at boot and exposes them at GET /api/v1/agent/contracts.
	agentContracts, err := agentcontract.LoadFS(os.DirFS(cfg.AgentContractsDir), ".")
	if err != nil {
		logger.Error("agent contracts load", "err", err)
		os.Exit(1)
	}
	logger.Info("agent contracts loaded", "summary", agentContracts.Summary())
	fySvc := fiscalyear.NewService(db)
	identitySvc := identity.NewService(db)
	identitySvc.SetPermissionEngine(perm) // so role/user permission edits invalidate the engine's in-process cache
	printRenderer := platformprint.NewGotenbergRenderer(cfg.GotenbergURL)
	printAdminSvc := platformprint.NewAdminService(db, printRenderer)
	workflowSvc := workflow.NewService(db)
	importSvc   := dataimport.NewService(db)
	webhookSvc  := webhooks.NewService(db)
	apiTokenSvc := apitokens.NewService(db)
	connectorSvc := connectors.NewService(db)
	notifRuleSvc := notifrules.NewService(db)
	notifier := notifrules.NewDispatcher(db, emailSvc)
	// Background worker drains notification_dispatch with exponential backoff.
	go notifier.RunWorker(ctx, 10*time.Second)
	// Partition manager keeps doc_event (monthly) + doc_view (daily) aligned.
	go audit.NewPartitionManager(db).Run(ctx)
	sysHealthSvc := sysinsights.NewService(db)
	payrollSetSvc := payrollconfig.NewService(db)
	approvalEng := workflow.NewApprovalEngine(db)
	approvalEng.Notifier = notifier
	workflowEng := workflow.NewEngine(db)
	siSvc.Approvals = approvalEng
	piSvc.Approvals = approvalEng
	peSvc.Approvals = approvalEng
	jeSvc.Approvals = approvalEng
	siSvc.Workflow = workflowEng
	piSvc.Workflow = workflowEng
	peSvc.Workflow = workflowEng
	jeSvc.Workflow = workflowEng
	siSvc.Notifier = notifier
	peSvc.Notifier = notifier
	pcvSvc.Approvals = approvalEng
	payrollSvc.Approvals = approvalEng
	bomSvc.Approvals = approvalEng
	woSvc.Approvals = approvalEng
	steSvc.Approvals = approvalEng
	assetSvc.Approvals = approvalEng

	r := chi.NewRouter()
	r.Use(httpx.RequestID)
	r.Use(httpx.AccessLog(logger))

	if len(cfg.CORSOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins:   cfg.CORSOrigins,
			AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders:   []string{"Authorization", "Content-Type", "X-Company-Id", "X-Request-Id", "Idempotency-Key"},
			ExposedHeaders:   []string{"X-Request-Id"},
			AllowCredentials: true,
			MaxAge:           600,
		}))
	}

	// Root → redirect to the interactive API docs (helpful when someone hits localhost:8080 in a browser).
	r.Get("/", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, "/api/v1/docs", http.StatusFound)
	})
	// Health / readiness / metrics — public, mounted at root.
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK); _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", func(w http.ResponseWriter, req *http.Request) {
		if err := db.Ping(req.Context()); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("not ready"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	publicPrefixes := []string{
		"/api/v1/auth/login",
		"/api/v1/auth/refresh",
		"/api/v1/auth/logout",
		"/api/v1/openapi",
		"/api/v1/docs",
		"/api/v1/schemas",
	}

	r.Route("/api/v1", func(api chi.Router) {
		api.Use(httpx.Auth(db, signer, publicPrefixes))
		api.Use(audit.RecordViewMiddleware(auditViewRec))

		humaCfg := huma.DefaultConfig("Logica ERP API", "0.1.0")
		humaCfg.OpenAPIPath = "/openapi"
		humaCfg.DocsPath = "/docs"
		humaCfg.SchemasPath = "/schemas"
		humaCfg.Servers = []*huma.Server{{URL: "/api/v1"}}

		hapi := humachi.New(api, humaCfg)
		auth.Register(hapi, &auth.Handler{
			DB:           db,
			Signer:       signer,
			RefreshTTL:   cfg.RefreshTokenTTL,
			CookieDomain: cfg.RefreshCookieDomain,
			CookieSecure: cfg.RefreshCookieSecure,
		})
		company.Register(hapi, &company.Handler{Service: companySvc, Perm: perm})
		account.Register(hapi, &account.Handler{Service: accountSvc, Perm: perm})
		journalentry.Register(hapi, &journalentry.Handler{Service: jeSvc, Perm: perm})

		customer.Register(hapi, &customer.Handler{Service: customerSvc, Perm: perm})
		supplier.Register(hapi, &supplier.Handler{Service: supplierSvc, Perm: perm})
		item.Register(hapi, &item.Handler{Service: itemSvc, Perm: perm})
		taxtemplate.Register(hapi, &taxtemplate.Handler{Service: taxSvc, Perm: perm})
		salesinvoice.Register(hapi, &salesinvoice.Handler{Service: siSvc, Perm: perm, DB: db, PrintRenderer: printRenderer, PrintAdmin: printAdminSvc})
		purchaseinvoice.Register(hapi, &purchaseinvoice.Handler{Service: piSvc, Perm: perm, DB: db, PrintAdmin: printAdminSvc})
		paymententry.Register(hapi, &paymententry.Handler{Service: peSvc, Perm: perm})
		reports.Register(hapi, &reports.Handler{Service: reportSvc, Perm: perm})
		periodclosing.Register(hapi, &periodclosing.Handler{Service: pcvSvc, Perm: perm})
		warehouse.Register(hapi, &warehouse.Handler{Service: whSvc, Perm: perm})
		stockentry.Register(hapi, &stockentry.Handler{Service: steSvc, Perm: perm})
		lead.Register(hapi, &lead.Handler{Service: leadSvc, Perm: perm})
		project.Register(hapi, &project.Handler{Service: projSvc, Perm: perm})
		bom.Register(hapi, &bom.Handler{Service: bomSvc, Perm: perm})
		workorder.Register(hapi, &workorder.Handler{Service: woSvc, Perm: perm})
		asset.Register(hapi, &asset.Handler{Service: assetSvc, Perm: perm})

		// Phase 5
		employee.Register(hapi, &employee.Handler{Service: empSvc, Perm: perm})
		hrpayroll.Register(hapi, &hrpayroll.Handler{Service: payrollSvc, Perm: perm})
		pos.Register(hapi, &pos.Handler{Service: posSvc, Perm: perm})
		issue.Register(hapi, &issue.Handler{Service: issueSvc, Perm: perm})

		// Phase 6
		efaktur.Register(hapi, &efaktur.Handler{Service: efakturSvc, Perm: perm})
		crosscut.Register(hapi, &crosscut.Handler{Service: crosscutSvc, Perm: perm})

		// Admin (settings)
		naming.RegisterAdmin(hapi, &naming.AdminHandler{Service: namingAdminSvc, Perm: perm})
		email.Register(hapi, &email.Handler{Service: emailSvc, Perm: perm})
		audit.RegisterAdmin(hapi, &audit.AdminHandler{Service: auditQuerySvc, Perm: perm})
		audit.RegisterTimeline(hapi, &audit.TimelineHandler{Service: timelineSvc, Perm: perm})
		fiscalyear.Register(hapi, &fiscalyear.Handler{Service: fySvc, Perm: perm})
		identity.Register(hapi, &identity.Handler{Service: identitySvc, Perm: perm})
		platformprint.RegisterAdmin(hapi, &platformprint.AdminHandler{Service: printAdminSvc, Perm: perm})
		workflow.Register(hapi, &workflow.Handler{Service: workflowSvc, Perm: perm})
		workflow.RegisterApprovals(hapi, &workflow.ApprovalHandler{Engine: approvalEng, Perm: perm})
		dataimport.Register(hapi, &dataimport.Handler{Service: importSvc, Perm: perm})
		webhooks.Register(hapi, &webhooks.Handler{Service: webhookSvc, Perm: perm})
		apitokens.Register(hapi, &apitokens.Handler{Service: apiTokenSvc, Perm: perm})
		connectors.Register(hapi, &connectors.Handler{Service: connectorSvc, Perm: perm})
		notifrules.Register(hapi, &notifrules.Handler{Service: notifRuleSvc, Perm: perm})
		sysinsights.Register(hapi, &sysinsights.Handler{Service: sysHealthSvc, Perm: perm})
		payrollconfig.Register(hapi, &payrollconfig.Handler{Service: payrollSetSvc, Perm: perm})
		agentcontract.Register(hapi, &agentcontract.Handler{Registry: agentContracts})
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("api: listening", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("api: serve", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	logger.Info("api: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
