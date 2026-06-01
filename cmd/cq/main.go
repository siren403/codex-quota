package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/siren403/codex-quota/internal/api"
	"github.com/siren403/codex-quota/internal/auth"
	"github.com/siren403/codex-quota/internal/config"
	"github.com/siren403/codex-quota/internal/ui"
	"github.com/siren403/codex-quota/internal/update"
	"github.com/siren403/codex-quota/internal/version"
)

type command int

const (
	commandRunTUI command = iota
	commandHelp
	commandVersion
	commandUpgrade
	commandLogin
	commandStatus
	commandAccounts
)

var (
	currentVersionFn   = version.Current
	detectUpdateMethod = update.DetectMethod
	fetchLatestVersion = update.FetchLatestVersion
	runUpgradeFn       = update.RunUpgrade
	runInteractiveFn   = runInteractive
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd, err := parseCommand(args)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n\n", err)
		io.WriteString(stderr, usageText())
		return 2
	}

	switch cmd {
	case commandHelp:
		io.WriteString(stdout, usageText())
		return 0
	case commandVersion:
		fmt.Fprintln(stdout, currentVersionFn())
		return 0
	case commandUpgrade:
		return runUpgradeCommand(stdout, stderr)
	case commandLogin:
		return runLoginCommand(stdout, stderr)
	case commandStatus:
		return runStatusCommand(stdout, stderr)
	case commandAccounts:
		return runAccountsCommand(stdout, stderr)
	default:
		if err := runInteractiveFn(stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}
}

func parseCommand(args []string) (command, error) {
	if len(args) == 0 {
		return commandRunTUI, nil
	}

	switch args[0] {
	case "--help", "-h", "help":
		return commandHelp, nil
	case "--version", "-version", "-v", "version":
		return commandVersion, nil
	case "upgrade":
		return commandUpgrade, nil
	case "login":
		return commandLogin, nil
	case "status":
		return commandStatus, nil
	case "accounts":
		return commandAccounts, nil
	default:
		return commandRunTUI, fmt.Errorf("unknown command or flag: %s", args[0])
	}
}

func usageText() string {
	return strings.TrimSpace(`
Usage:
  cq
  cq --help
  cq --version
  cq login
  cq status
  cq accounts
  cq upgrade

Commands:
  (none)      Launch interactive TUI
  login       Add account via device auth (headless)
  status      Print quota status to stdout
  accounts    List saved accounts
  --version   Print the current cq version
  upgrade     Upgrade cq when the install method is known
`) + "\n"
}

// runLoginCommand runs device auth without the TUI.
// Shows the verification URL and user code, then polls until done.
func runLoginCommand(stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, "Starting device login...")

	status, err := auth.StartDeviceLogin()
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout)
	fmt.Fprintf(stdout, "  Open:  %s\n", status.VerifyURL)
	fmt.Fprintf(stdout, "  Code:  %s\n", status.UserCode)
	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Waiting for authorization (timeout: 15 min)...")

	for {
		time.Sleep(300 * time.Millisecond)
		account, done, err := auth.PollDeviceLogin()
		if !done {
			continue
		}
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		if err := config.UpsertManagedAccount(account); err != nil {
			fmt.Fprintf(stderr, "error saving account: %v\n", err)
			return 1
		}
		label := account.Email
		if label == "" {
			label = account.AccountID
		}
		fmt.Fprintf(stdout, "Logged in as: %s\n", label)
		return 0
	}
}

// runStatusCommand prints quota status for all accounts to stdout.
func runStatusCommand(stdout, stderr io.Writer) int {
	result, err := config.LoadAllAccountsWithSources()
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to load accounts: %v\n", err)
		return 1
	}
	if len(result.Accounts) == 0 {
		fmt.Fprintln(stdout, "No accounts found. Run: cq login")
		return 0
	}

	for _, account := range result.Accounts {
		label := account.Email
		if label == "" {
			label = account.AccountID
		}
		if label == "" {
			label = "(unknown)"
		}
		fmt.Fprintf(stdout, "Account: %s\n", label)

		if auth.IsExpired(account) {
			if err := auth.RefreshToken(account); err != nil {
				fmt.Fprintf(stderr, "  warning: token refresh failed: %v\n", err)
			}
		}

		data, err := api.CallAPI(account.AccessToken, account.AccountID)
		if err != nil {
			fmt.Fprintf(stdout, "  error: %v\n\n", err)
			continue
		}

		fmt.Fprintf(stdout, "  plan:          %s\n", data.PlanType)
		fmt.Fprintf(stdout, "  allowed:       %v\n", data.Allowed)
		fmt.Fprintf(stdout, "  limit_reached: %v\n", data.LimitReached)
		for _, w := range data.Windows {
			reset := ""
			if !w.ResetAt.IsZero() {
				reset = fmt.Sprintf(" (resets %s)", w.ResetAt.Format("15:04 MST"))
			}
			fmt.Fprintf(stdout, "  %-26s used=%.0f%%  left=%.0f%%%s\n",
				w.Label+":", w.UsedPercent, w.LeftPercent, reset)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

// runAccountsCommand lists saved accounts.
func runAccountsCommand(stdout, stderr io.Writer) int {
	result, err := config.LoadAllAccountsWithSources()
	if err != nil {
		fmt.Fprintf(stderr, "error: failed to load accounts: %v\n", err)
		return 1
	}
	if len(result.Accounts) == 0 {
		fmt.Fprintln(stdout, "No accounts found. Run: cq login")
		return 0
	}
	for i, account := range result.Accounts {
		label := account.Email
		if label == "" {
			label = account.AccountID
		}
		if label == "" {
			label = "(unknown)"
		}
		sources := result.SourcesByAccountID[account.AccountID]
		sourceStr := strings.Join(sources, ", ")
		if sourceStr == "" {
			sourceStr = string(account.Source)
		}
		fmt.Fprintf(stdout, "[%d] %s  (source: %s)\n", i+1, label, sourceStr)
	}
	return 0
}

func runUpgradeCommand(stdout, stderr io.Writer) int {
	method := detectUpdateMethod()
	currentVersion := currentVersionFn()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	latestVersion, latestErr := fetchLatestVersion(ctx, method)
	if method == update.MethodUnknown {
		if latestErr != nil {
			fmt.Fprintf(stderr, "warning: failed to resolve latest version: %v\n", latestErr)
		}
		fmt.Fprintln(stdout, update.ManualUpgradeInstructions(currentVersion, latestVersion))
		return 1
	}

	if latestErr == nil && !update.IsNewer(latestVersion, currentVersion) {
		fmt.Fprintf(stdout, "cq is already up to date (%s)\n", currentVersion)
		return 0
	}

	if latestErr != nil {
		fmt.Fprintf(stderr, "warning: failed to resolve latest version: %v\n", latestErr)
	}

	if err := runUpgradeFn(method, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}

	return 0
}

func runInteractive(stdout, stderr io.Writer) error {
	loadResult, err := config.LoadAllAccountsWithSources()
	if err != nil {
		return fmt.Errorf("failed to load accounts: %w", err)
	}

	uiState, uiStateErr := config.LoadUIState()
	if uiStateErr != nil {
		fmt.Fprintf(stderr, "warning: failed to load ui state: %v\n", uiStateErr)
	}

	settings, settingsErr := config.LoadSettings()
	if settingsErr != nil {
		fmt.Fprintf(stderr, "warning: failed to load settings: %v\n", settingsErr)
		settings = config.DefaultSettings()
	}

	updateState, updateStateErr := config.LoadUpdateState()
	if updateStateErr != nil {
		fmt.Fprintf(stderr, "warning: failed to load update state: %v\n", updateStateErr)
	}

	method := detectUpdateMethod()
	currentVersion := currentVersionFn()
	var startupUpdate *ui.StartupUpdatePrompt
	if latestVersion, ok := update.ShouldPrompt(settings, updateState, currentVersion, method); ok {
		startupUpdate = &ui.StartupUpdatePrompt{
			Version: latestVersion,
			Method:  method,
		}
	}

	program := tea.NewProgram(
		ui.InitialModelWithStartupUpdate(
			loadResult.Accounts,
			loadResult.SourcesByAccountID,
			loadResult.ActiveSourcesByIdentity,
			uiState,
			startupUpdate,
		),
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	if settings.CheckForUpdateOnStartup && update.ShouldRefresh(updateState, time.Now()) {
		startBackgroundRefresh(program, updateState, method, currentVersion, settings)
	}
	model, err := program.Run()
	if err != nil {
		return err
	}

	finalModel, ok := model.(ui.Model)
	if !ok {
		return nil
	}

	if pendingMethod, ok := finalModel.PendingUpdate(); ok {
		return runUpgradeFn(pendingMethod, stdout, stderr)
	}

	return nil
}

func startBackgroundRefresh(program *tea.Program, state config.UpdateState, method update.Method, currentVersion string, settings config.Settings) {
	go func(prev config.UpdateState, detectedMethod update.Method) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		refreshed, err := update.RefreshState(ctx, prev, detectedMethod)
		if err != nil {
			return
		}

		if currentState, err := config.LoadUpdateState(); err == nil {
			refreshed.DismissedVersion = currentState.DismissedVersion
		}

		if err := config.SaveUpdateState(refreshed); err != nil {
			return
		}

		if latestVersion, ok := update.ShouldPrompt(settings, refreshed, currentVersion, detectedMethod); ok && program != nil {
			program.Send(ui.UpdateAvailableMsg{Version: latestVersion, Method: detectedMethod})
		}
	}(state, method)
}
