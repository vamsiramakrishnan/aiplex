package main

import "github.com/charmbracelet/lipgloss"

var (
	// Status indicators
	passStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	failStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	warnStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true)
	infoStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

	// Section headers
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12")).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12"))

	// Step labels [1/6]
	stepStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("14"))

	// Dim text for secondary info
	dimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	// Success box
	successBoxStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("10")).
			Padding(0, 1).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("10"))
)

// Styled status indicators
func pass(msg string) string { return passStyle.Render("✓") + " " + msg }
func fail(msg string) string { return failStyle.Render("✗") + " " + msg }
func warn(msg string) string { return warnStyle.Render("!") + " " + msg }
func info(msg string) string { return infoStyle.Render("●") + " " + msg }
func step(label string) string { return stepStyle.Render(label) }
func dim(msg string) string  { return dimStyle.Render(msg) }
