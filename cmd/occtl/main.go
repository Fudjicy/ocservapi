package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/example/ocservapi/internal/cli"
	"github.com/example/ocservapi/internal/store"
)

const (
	colorBlue  = "\033[34m"
	colorGreen = "\033[32m"
	colorCyan  = "\033[36m"
	colorDim   = "\033[2m"
	colorReset = "\033[0m"
)

func main() {
	api := flag.String("api", envOrDefault("OCCTL_API", "http://127.0.0.1:8080"), "API base URL")
	sessionPath := flag.String("session", cli.DefaultSessionPath(), "session file")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	switch args[0] {
	case "auth":
		handleAuth(ctx, *api, *sessionPath, args[1:])
	case "system":
		handleSystem(ctx, *api, *sessionPath, args[1:])
	case "endpoint":
		handleEndpoint(ctx, *api, *sessionPath, args[1:])
	case "deployment":
		handleDeployment(ctx, *api, *sessionPath, args[1:])
	case "audit":
		handleAudit(ctx, *api, *sessionPath, args[1:])
	case "shell", "tui":
		handleTUI(*api, *sessionPath)
	default:
		printUsage()
		os.Exit(1)
	}
}

func handleAuth(ctx context.Context, api, sessionPath string, args []string) {
	if len(args) == 0 {
		fatal("missing auth subcommand")
	}
	switch args[0] {
	case "login":
		fs := flag.NewFlagSet("auth login", flag.ExitOnError)
		username := fs.String("username", "owner", "username")
		_ = fs.Parse(args[1:])
		client := cli.NewClient(api, "")
		token, user, err := client.Login(ctx, *username)
		if err != nil {
			fatal(err.Error())
		}
		if err := cli.SaveSession(sessionPath, cli.Session{API: api, Token: token}); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("logged in as %s (%s)\n", user.Username, user.Role)
	case "whoami":
		client := mustAuthedClient(api, sessionPath)
		user, err := client.WhoAmI(ctx)
		if err != nil {
			fatal(err.Error())
		}
		printJSON(user)
	default:
		fatal("unknown auth subcommand")
	}
}

func handleSystem(ctx context.Context, api, sessionPath string, args []string) {
	if len(args) == 0 || args[0] != "info" {
		fatal("usage: occtl system info")
	}
	client := mustAuthedClient(api, sessionPath)
	info, err := client.SystemInfo(ctx)
	if err != nil {
		fatal(err.Error())
	}
	printJSON(info)
}

func handleEndpoint(ctx context.Context, api, sessionPath string, args []string) {
	if len(args) == 0 || args[0] != "list" {
		fatal("usage: occtl endpoint list")
	}
	client := mustAuthedClient(api, sessionPath)
	items, err := client.Endpoints(ctx)
	if err != nil {
		fatal(err.Error())
	}
	printJSON(items)
}

func handleDeployment(ctx context.Context, api, sessionPath string, args []string) {
	if len(args) == 0 || args[0] != "list" {
		fatal("usage: occtl deployment list")
	}
	client := mustAuthedClient(api, sessionPath)
	items, err := client.Deployments(ctx)
	if err != nil {
		fatal(err.Error())
	}
	printJSON(items)
}

func handleAudit(ctx context.Context, api, sessionPath string, args []string) {
	if len(args) == 0 || args[0] != "list" {
		fatal("usage: occtl audit list")
	}
	client := mustAuthedClient(api, sessionPath)
	items, err := client.Audit(ctx)
	if err != nil {
		fatal(err.Error())
	}
	printJSON(items)
}

func handleTUI(api, sessionPath string) {
	client := mustAuthedClient(api, sessionPath)
	reader := bufio.NewReader(os.Stdin)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		user, err := client.WhoAmI(ctx)
		if err != nil {
			cancel()
			fatal(err.Error())
		}
		info, err := client.SystemInfo(ctx)
		if err != nil {
			cancel()
			fatal(err.Error())
		}
		endpoints, err := client.Endpoints(ctx)
		if err != nil {
			cancel()
			fatal(err.Error())
		}
		deployments, err := client.Deployments(ctx)
		if err != nil {
			cancel()
			fatal(err.Error())
		}
		auditEvents, err := client.Audit(ctx)
		if err != nil {
			cancel()
			fatal(err.Error())
		}
		access, err := client.Access(ctx)
		if err != nil {
			cancel()
			fatal(err.Error())
		}
		health, err := client.Health(ctx)
		cancel()
		if err != nil {
			fatal(err.Error())
		}

		fmt.Print("\033[H\033[2J")
		fmt.Println(colorBlue + "occtl local console" + colorReset)
		fmt.Printf("%sSystem:%s %s\n", colorCyan, colorReset, info.DisplayName)
		fmt.Printf("%sInstallation ID:%s %s\n", colorCyan, colorReset, info.InstallationID)
		fmt.Printf("%sAPI:%s %s\n", colorCyan, colorReset, api)
		fmt.Printf("%sAPI status:%s %v\n", colorCyan, colorReset, health["status"])
		fmt.Printf("%sDB status:%s %s\n", colorCyan, colorReset, info.DBStatus)
		fmt.Printf("%sVisible endpoints:%s %d\n", colorCyan, colorReset, len(endpoints))
		fmt.Printf("%sCurrent user:%s %s\n", colorCyan, colorReset, user.Username)
		fmt.Printf("%sRole:%s %s\n\n", colorCyan, colorReset, user.Role)

		fmt.Println(colorGreen + "Sections" + colorReset)
		fmt.Println("1) Dashboard")
		fmt.Println("2) Endpoints")
		fmt.Println("3) Endpoint details")
		fmt.Println("4) Deployments")
		fmt.Println("5) Certificates")
		fmt.Println("6) Access / Permissions")
		fmt.Println("7) Audit log")
		fmt.Println("8) System info")
		fmt.Println("q) Quit")
		fmt.Print("\nSelect section: ")
		choice, _ := reader.ReadString('\n')
		choice = strings.TrimSpace(choice)
		fmt.Print("\033[H\033[2J")
		switch choice {
		case "1":
			fmt.Printf("%sDashboard%s\nSystem: %s\nInstallation ID: %s\nAPI: %s\nAPI status: %v\nDB status: %s\nVisible endpoints: %d\nCurrent user: %s\nRole: %s\n", colorGreen, colorReset, info.DisplayName, info.InstallationID, api, health["status"], info.DBStatus, len(endpoints), user.Username, user.Role)
		case "2":
			fmt.Printf("%sEndpoints%s\n", colorGreen, colorReset)
			for _, ep := range endpoints {
				fmt.Printf("- %s (%s) — %s\n", ep.Name, ep.Address, ep.Description)
			}
		case "3":
			fmt.Printf("%sEndpoint details%s\n", colorGreen, colorReset)
			if len(endpoints) == 0 {
				fmt.Println("No endpoints available.")
			} else {
				ep := endpoints[0]
				fmt.Printf("Name: %s\nAddress: %s\nDescription: %s\nCreated: %s\n", ep.Name, ep.Address, ep.Description, ep.CreatedAt.Format(time.RFC3339))
			}
		case "4":
			fmt.Printf("%sDeployments%s\n", colorGreen, colorReset)
			for _, d := range deployments {
				ep := "-"
				if d.Endpoint != nil {
					ep = *d.Endpoint
				}
				fmt.Printf("- [%s] %s on %s — %s\n", d.Status, d.Operation, ep, d.Summary)
			}
		case "5":
			fmt.Printf("%sCertificates%s\nTotal certificates: %d\n", colorGreen, colorReset, info.CertificateCount)
		case "6":
			fmt.Printf("%sAccess / Permissions%s\n", colorGreen, colorReset)
			for _, item := range access {
				fmt.Printf("- %s: view=%t inspect=%t deploy=%t manage_users=%t manage_certs=%t manage_endpoint=%t\n", item.EndpointName, item.CanView, item.CanInspect, item.CanDeploy, item.CanManageUsers, item.CanManageCertificates, item.CanManageEndpoint)
			}
		case "7":
			fmt.Printf("%sAudit log%s\n", colorGreen, colorReset)
			for _, item := range auditEvents {
				fmt.Printf("- [%s] %s — %s\n", item.Result, item.Action, item.Message)
			}
		case "8":
			fmt.Printf("%sSystem info%s\nInstallation ID: %s\nDisplay name: %s\nSchema version: %d\nSafe DSN: %s\nData dir: %s\nMaster key loaded: %t\nEndpoints: %d\nAdmins: %d\nDeployments: %d\nAudit records: %d\n", colorGreen, colorReset, info.InstallationID, info.DisplayName, info.SchemaVersion, info.SafeDSN, info.DataDir, info.MasterKeyLoaded, info.EndpointCount, info.AdminCount, info.DeploymentCount, info.AuditCount)
		case "q", "Q":
			return
		default:
			fmt.Println("Unknown section.")
		}
		fmt.Println("\n" + colorDim + "Press Enter to continue..." + colorReset)
		_, _ = reader.ReadString('\n')
	}
}

func mustAuthedClient(api, sessionPath string) *cli.Client {
	session, err := cli.LoadSession(sessionPath)
	if err != nil {
		fatal(fmt.Sprintf("load session: %v", err))
	}
	baseURL := api
	if session.API != "" {
		baseURL = session.API
	}
	return cli.NewClient(baseURL, session.Token)
}

func printJSON(v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fatal(err.Error())
	}
	fmt.Println(string(data))
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func printUsage() {
	fmt.Println(`occtl commands:
  occtl auth login --username owner
  occtl auth whoami
  occtl system info
  occtl endpoint list
  occtl deployment list
  occtl audit list
  occtl shell
  occtl tui`)
}

var _ = store.Endpoint{}
