package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/vamsiramakrishnan/aiplex/sdk/aiplex"
)

func tuiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tui",
		Short: "Interactive terminal dashboard",
		Long:  "Full-screen terminal UI for monitoring and managing AIPlex resources.",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			p := tea.NewProgram(newTUIModel(c), tea.WithAltScreen())
			_, err := p.Run()
			return err
		},
	}
}

// ─── Messages ──────────────────────────────────────────

type statsMsg struct {
	stats *aiplex.DashboardStats
	err   error
}

type instancesMsg struct {
	instances []aiplex.Instance
	err       error
}

type agentsMsg struct {
	agents []aiplex.Agent
	err    error
}

type catalogMsg struct {
	page *aiplex.CatalogPage
	err  error
}

// ─── Model ─────────────────────────────────────────────

type tuiModel struct {
	client    *aiplex.Client
	activeTab int
	tabs      []string

	// Data
	stats     *aiplex.DashboardStats
	instances []aiplex.Instance
	agents    []aiplex.Agent
	catalog   *aiplex.CatalogPage

	// Tables
	instanceTable table.Model
	agentTable    table.Model
	catalogTable  table.Model

	// State
	loading bool
	err     error
	width   int
	height  int
}

func newTUIModel(c *aiplex.Client) tuiModel {
	tabs := []string{"Dashboard", "Instances", "Agents", "Catalog"}

	// Instance table
	instCols := []table.Column{
		{Title: "Name", Width: 25},
		{Title: "Plane", Width: 10},
		{Title: "Template", Width: 20},
		{Title: "Status", Width: 10},
		{Title: "Owner", Width: 20},
	}
	instTable := table.New(table.WithColumns(instCols), table.WithHeight(15))
	s := table.DefaultStyles()
	s.Header = s.Header.Bold(true).Foreground(lipgloss.Color("12"))
	s.Selected = s.Selected.Foreground(lipgloss.Color("15")).Background(lipgloss.Color("4"))
	instTable.SetStyles(s)

	// Agent table
	agentCols := []table.Column{
		{Title: "Client ID", Width: 25},
		{Title: "Display Name", Width: 25},
		{Title: "Auth Method", Width: 18},
		{Title: "Scopes", Width: 8},
	}
	agentTable := table.New(table.WithColumns(agentCols), table.WithHeight(15))
	agentTable.SetStyles(s)

	// Catalog table
	catalogCols := []table.Column{
		{Title: "Name", Width: 30},
		{Title: "Plane", Width: 10},
		{Title: "Source", Width: 20},
		{Title: "Description", Width: 50},
	}
	catalogTable := table.New(table.WithColumns(catalogCols), table.WithHeight(15))
	catalogTable.SetStyles(s)

	return tuiModel{
		client:        c,
		tabs:          tabs,
		instanceTable: instTable,
		agentTable:    agentTable,
		catalogTable:  catalogTable,
		loading:       true,
	}
}

// ─── Commands ──────────────────────────────────────────

func fetchStats(c *aiplex.Client) tea.Cmd {
	return func() tea.Msg {
		stats, err := c.GetDashboardStats(context.Background())
		return statsMsg{stats, err}
	}
}

func fetchInstances(c *aiplex.Client) tea.Cmd {
	return func() tea.Msg {
		instances, err := c.ListInstances(context.Background(), nil)
		return instancesMsg{instances, err}
	}
}

func fetchAgents(c *aiplex.Client) tea.Cmd {
	return func() tea.Msg {
		agents, err := c.ListAgents(context.Background())
		return agentsMsg{agents, err}
	}
}

func fetchCatalog(c *aiplex.Client) tea.Cmd {
	return func() tea.Msg {
		page, err := c.ListCatalog(context.Background(), nil)
		return catalogMsg{page, err}
	}
}

// ─── Bubble Tea Interface ──────────────────────────────

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(
		fetchStats(m.client),
		fetchInstances(m.client),
		fetchAgents(m.client),
		fetchCatalog(m.client),
	)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.activeTab = (m.activeTab + 1) % len(m.tabs)
			return m, nil
		case "shift+tab":
			m.activeTab = (m.activeTab - 1 + len(m.tabs)) % len(m.tabs)
			return m, nil
		case "r":
			m.loading = true
			return m, tea.Batch(
				fetchStats(m.client),
				fetchInstances(m.client),
				fetchAgents(m.client),
				fetchCatalog(m.client),
			)
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case statsMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
		} else {
			m.stats = msg.stats
		}
		return m, nil

	case instancesMsg:
		if msg.err == nil {
			m.instances = msg.instances
			rows := make([]table.Row, len(msg.instances))
			for i, inst := range msg.instances {
				displayName := inst.DisplayName
				if displayName == "" {
					displayName = inst.ID
				}
				rows[i] = table.Row{
					displayName,
					inst.Plane,
					inst.TemplateID,
					inst.Status,
					inst.Owner,
				}
			}
			m.instanceTable.SetRows(rows)
		}
		return m, nil

	case agentsMsg:
		if msg.err == nil {
			m.agents = msg.agents
			rows := make([]table.Row, len(msg.agents))
			for i, a := range msg.agents {
				rows[i] = table.Row{
					a.ClientID,
					a.DisplayName,
					a.AuthMethod,
					fmt.Sprintf("%d", len(a.AllowedScopes)),
				}
			}
			m.agentTable.SetRows(rows)
		}
		return m, nil

	case catalogMsg:
		if msg.err == nil {
			m.catalog = msg.page
			rows := make([]table.Row, len(msg.page.Templates))
			for i, t := range msg.page.Templates {
				desc := t.Description
				if len(desc) > 50 {
					desc = desc[:47] + "..."
				}
				rows[i] = table.Row{
					t.Name,
					t.Plane,
					t.Source,
					desc,
				}
			}
			m.catalogTable.SetRows(rows)
		}
		return m, nil
	}

	// Forward key events to active table
	var cmd tea.Cmd
	switch m.activeTab {
	case 1:
		m.instanceTable, cmd = m.instanceTable.Update(msg)
	case 2:
		m.agentTable, cmd = m.agentTable.Update(msg)
	case 3:
		m.catalogTable, cmd = m.catalogTable.Update(msg)
	}
	return m, cmd
}

func (m tuiModel) View() string {
	if m.width == 0 {
		return "Loading..."
	}

	var b strings.Builder

	// Header
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("12")).
		Padding(0, 1).
		Render("AIPlex")

	b.WriteString(title)
	b.WriteString("\n\n")

	// Tab bar
	for i, tab := range m.tabs {
		style := lipgloss.NewStyle().Padding(0, 2)
		if i == m.activeTab {
			style = style.Bold(true).
				Foreground(lipgloss.Color("15")).
				Background(lipgloss.Color("4"))
		} else {
			style = style.Foreground(lipgloss.Color("8"))
		}
		b.WriteString(style.Render(tab))
		b.WriteString(" ")
	}
	b.WriteString("\n\n")

	// Content
	switch m.activeTab {
	case 0:
		b.WriteString(m.renderDashboard())
	case 1:
		b.WriteString(m.instanceTable.View())
	case 2:
		b.WriteString(m.agentTable.View())
	case 3:
		b.WriteString(m.catalogTable.View())
	}

	// Footer
	b.WriteString("\n\n")
	footer := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(
		"  tab: switch panels  •  r: refresh  •  ↑↓: navigate  •  q: quit")
	b.WriteString(footer)

	return b.String()
}

func (m tuiModel) renderDashboard() string {
	if m.err != nil {
		return failStyle.Render(fmt.Sprintf("  Error: %v\n\n  Check: aiplex health", m.err))
	}
	if m.stats == nil {
		return dimStyle.Render("  Loading...")
	}

	s := m.stats
	var b strings.Builder

	// Stats grid
	statStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(0, 2).
		Width(22)

	row1 := lipgloss.JoinHorizontal(lipgloss.Top,
		statStyle.Render(fmt.Sprintf("Instances\n%s", lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d running / %d total", s.RunningInstances, s.TotalInstances)))),
		statStyle.Render(fmt.Sprintf("Agents\n%s", lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d registered", s.RegisteredAgents)))),
		statStyle.Render(fmt.Sprintf("Planes\n%s", lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("%d active", s.ActivePlanes)))),
	)
	b.WriteString(row1)
	b.WriteString("\n\n")

	// Per-plane breakdown
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("  Per Plane"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  MCPlex:   %d instances\n", s.MCPlexInstances))
	b.WriteString(fmt.Sprintf("  A2APlex:  %d instances\n", s.A2APlexInstances))
	b.WriteString(fmt.Sprintf("  LLMPlex:  %d instances\n", s.LLMPlexInstances))
	b.WriteString("\n")

	// 24h metrics
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("  Last 24h"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  LLM Cost:        $%.2f\n", s.DailyCostUSD))
	b.WriteString(fmt.Sprintf("  LLM Tokens:      %d\n", s.DailyTokens))
	b.WriteString(fmt.Sprintf("  LLM Requests:    %d\n", s.DailyRequests))
	b.WriteString(fmt.Sprintf("  Tool Calls:      %d\n", s.ToolCalls))
	b.WriteString(fmt.Sprintf("  A2A Delegations: %d\n", s.A2ADelegations))
	if s.PolicyDenials > 0 {
		b.WriteString(fmt.Sprintf("  Policy Denials:  %s\n", failStyle.Render(fmt.Sprintf("%d", s.PolicyDenials))))
	} else {
		b.WriteString(fmt.Sprintf("  Policy Denials:  %d\n", s.PolicyDenials))
	}

	return b.String()
}
