// Command server is the Dad Joke control-plane HTTP server.
//
//	server                     run HTTP (:8080) with ControlAPI + AgentAPI
//	server bootstrap --tenant  create a tenant and first dj_pat_ API key
//	server create-user         create a tenant + email/password user
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/barzoj/dad-joke/internal/bootstrap"
	"github.com/barzoj/dad-joke/internal/createuser"
	"github.com/barzoj/dad-joke/internal/server"
	mongostore "github.com/barzoj/dad-joke/internal/store/mongo"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "bootstrap":
			return runBootstrap(args[1:])
		case "create-user":
			return runCreateUser(args[1:])
		}
	}
	return runServe(args)
}

func mongoConfigFromEnv() mongostore.Config {
	uri := strings.TrimSpace(os.Getenv("DAD_JOKE_MONGODB_URI"))
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017"
	}
	db := strings.TrimSpace(os.Getenv("DAD_JOKE_MONGODB_DATABASE"))
	return mongostore.Config{URI: uri, Database: db}
}

func runServe(args []string) int {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	addr := fs.String("addr", envOr("DAD_JOKE_HTTP_ADDR", ":8080"), "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	st, err := mongostore.Open(openCtx, mongoConfigFromEnv())
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: open store: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close(context.Background()) }()

	if err := st.EnsureIndexes(openCtx); err != nil {
		fmt.Fprintf(os.Stderr, "server: ensure indexes: %v\n", err)
		return 1
	}

	allowSignup := envBool("ALLOW_SIGNUP", true)
	cookieSecure := envBool("COOKIE_SECURE", true)
	srv, err := server.New(server.Config{
		Addr:         *addr,
		Store:        st,
		Log:          log.New(os.Stderr, "server: ", log.LstdFlags|log.Lmsgprefix),
		AllowSignup:  &allowSignup,
		CookieSecure: &cookieSecure,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		return 1
	}

	log.Printf("listening on %s", *addr)
	if err := srv.ListenAndServe(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		return 1
	}
	return 0
}

func runBootstrap(args []string) int {
	fs := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tenant := fs.String("tenant", "", "tenant name to create")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*tenant) == "" {
		fmt.Fprintln(os.Stderr, "bootstrap: --tenant is required")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := mongostore.Open(ctx, mongoConfigFromEnv())
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: open store: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close(context.Background()) }()

	if err := st.EnsureIndexes(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: ensure indexes: %v\n", err)
		return 1
	}

	res, err := bootstrap.Run(ctx, st, *tenant)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: %v\n", err)
		return 1
	}
	// Raw key printed once to stdout only — never to logs.
	if err := bootstrap.PrintRawKey(os.Stdout, res.RawKey); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap: write key: %v\n", err)
		return 1
	}
	return 0
}

func runCreateUser(args []string) int {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	email := fs.String("email", "", "user email")
	passFlag := fs.String("password", "", "user password (prefer DAD_JOKE_CREATE_USER_PASSWORD env)")
	tenantName := fs.String("tenant-name", "", "optional tenant name (default: email)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if strings.TrimSpace(*email) == "" {
		fmt.Fprintln(os.Stderr, "create-user: --email is required")
		return 2
	}
	pass := strings.TrimSpace(*passFlag)
	if pass == "" {
		pass = strings.TrimSpace(os.Getenv("DAD_JOKE_CREATE_USER_PASSWORD"))
	}
	if pass == "" {
		fmt.Fprintln(os.Stderr, "create-user: password via --password or DAD_JOKE_CREATE_USER_PASSWORD is required")
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st, err := mongostore.Open(ctx, mongoConfigFromEnv())
	if err != nil {
		fmt.Fprintf(os.Stderr, "create-user: open store: %v\n", err)
		return 1
	}
	defer func() { _ = st.Close(context.Background()) }()

	if err := st.EnsureIndexes(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "create-user: ensure indexes: %v\n", err)
		return 1
	}

	res, err := createuser.Run(ctx, st, *email, pass, *tenantName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create-user: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(os.Stdout, "created user %s (tenant %s)\n", res.UserID, res.TenantID)
	return 0
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}
