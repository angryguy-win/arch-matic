package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"embed"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

//go:embed install
var installFiles embed.FS

//go:embed scripts/install.sh
var installScript string

// Nordic theme colors
var (
	nord0  = lipgloss.Color("#2E3440")
	nord1  = lipgloss.Color("#3B4252")
	nord2  = lipgloss.Color("#434C5E")
	nord3  = lipgloss.Color("#4C566A")
	nord4  = lipgloss.Color("#D8DEE9")
	nord5  = lipgloss.Color("#E5E9F0")
	nord6  = lipgloss.Color("#ECEFF4")
	nord7  = lipgloss.Color("#8FBCBB")
	nord8  = lipgloss.Color("#88C0D0")
	nord9  = lipgloss.Color("#81A1C1")
	nord10 = lipgloss.Color("#5E81AC")
	nord11 = lipgloss.Color("#BF616A")
	nord12 = lipgloss.Color("#D08770")
	nord13 = lipgloss.Color("#EBCB8B")
	nord14 = lipgloss.Color("#A3BE8C")
	nord15 = lipgloss.Color("#B48EAD")
)

type model struct {
	questions        []Question
	currentIndex     int
	answers          map[string]string
	width, height    int
	textInput        textinput.Model
	errorMsg         string
	confirmationMode bool
	listItems        []string
	selectedItem     int
}

type Question struct {
	ID       string
	Text     string
	Type     string // "text", "password", "yesno", "select", "multiselect"
	Options  []string
	Answer   string
	Validate func(string, map[string]string) error
}

type Contact struct {
	// Define the fields of your Contact struct here
	Name  string
	Email string
	// ... other fields ...
}

func getDriveInfo() []string {
	cmd := exec.Command("bash", "-c", "lsblk -ndo NAME,SIZE,MODEL")
	output, err := cmd.Output()
	if err != nil {
		fmt.Printf("Error getting drive info: %v\n", err)
		return []string{"/dev/sda (Unknown size) Unknown model"}
	}

	var drives []string
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 2 {
			drive := fmt.Sprintf("/dev/%s (%s", fields[0], fields[1])
			if len(fields) >= 3 {
				drive += fmt.Sprintf(" - %s", strings.Join(fields[2:], " "))
			}
			drive += ")"
			drives = append(drives, drive)
		}
	}

	if len(drives) == 0 {
		return []string{"/dev/sda (Unknown size) Unknown model"}
	}
	return drives
}

func getCPUInfo() (cpuType string, vendor string, microcode string, numCPUs int) {
	cpuType = "Unknown"
	vendor = "Unknown"
	microcode = "Unknown"
	numCPUs = runtime.NumCPU()

	cmd := exec.Command("sh", "-c", "lscpu | grep -E 'Model name|Vendor ID|Microcode'")
	output, err := cmd.Output()
	if err == nil {
		lines := strings.Split(string(output), "\n")
		for _, line := range lines {
			if strings.Contains(line, "Model name") {
				cpuType = strings.TrimSpace(strings.Split(line, ":")[1])
			} else if strings.Contains(line, "Vendor ID") {
				vendorFull := strings.TrimSpace(strings.Split(line, ":")[1])
				if strings.Contains(vendorFull, "AuthenticAMD") {
					vendor = "amd"
				} else if strings.Contains(vendorFull, "GenuineIntel") {
					vendor = "intel"
				}
			} else if strings.Contains(line, "Microcode") {
				microcode = strings.TrimSpace(strings.Split(line, ":")[1])
			}
		}
	}

	// If microcode is still unknown, try alternative method for AMD
	if microcode == "Unknown" && vendor == "amd" {
		cmd := exec.Command("sh", "-c", "grep -m1 'microcode' /proc/cpuinfo | awk '{print $3}'")
		output, err := cmd.Output()
		if err == nil {
			microcode = strings.TrimSpace(string(output))
		}
	}
	return
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Type here..."
	ti.Focus()

	// Initialize the list select component
	l := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(false)

	driveOptions := getDriveInfo()

	cpuType, cpuVendor, microcode, numCPUs := getCPUInfo()
	osInfo := fmt.Sprintf("OS: %s, Architecture: %s", runtime.GOOS, runtime.GOARCH)

	var mem syscall.Sysinfo_t
	syscall.Sysinfo(&mem)
	totalRAM := mem.Totalram * uint64(mem.Unit) / (1024 * 1024 * 1024) // Convert to GB

	timezone := []string{"UTC", "America/New_York", "America/Toronto", "America/Vancouver", "America/Chicago", "America/Denver",
		"America/Los_Angeles", "America/Texas", "Europe/London", "Europe/Berlin", "Asia/Tokyo"}
	locale := []string{"en_US.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8"}
	keymap := []string{"us", "uk", "de"}
	filesystem := []string{"btrfs", "ext4"}
	desktop_env := []string{"none", "gnome", "kde", "cosmic", "dwm"}

	m := model{
		questions: []Question{
			{ID: "username", Text: "Enter username:", Type: "text", Validate: validateUsername},
			{ID: "password", Text: "Enter password:", Type: "password", Validate: validatePassword},
			{ID: "confirm_password", Text: "Confirm password:", Type: "password", Validate: validateConfirmPassword},
			{ID: "hostname", Text: "Enter hostname:", Type: "text", Validate: validateHostname},
			{ID: "timezone", Text: "Select timezone:", Type: "select", Options: timezone},
			{ID: "locale", Text: "Select locale:", Type: "select", Options: locale},
			{ID: "root_password", Text: "Use same password for root?", Type: "yesno"},
			{ID: "disk_encryption", Text: "Use disk encryption?", Type: "yesno"},
			{ID: "keymap", Text: "Select keymap:", Type: "select", Options: keymap},
			{ID: "filesystem", Text: "Select filesystem:", Type: "select", Options: filesystem},
			{ID: "desktop_env", Text: "Select desktop environment:", Type: "select", Options: desktop_env},
			{ID: "install_drive", Text: "Select installation drive:", Type: "select", Options: driveOptions},
			{ID: "cpu_type", Text: "CPU Type:", Type: "text", Answer: cpuType},
			{ID: "cpu_vendor", Text: "CPU Vendor:", Type: "text", Answer: cpuVendor},
			{ID: "cpu_microcode", Text: "CPU Microcode:", Type: "text", Answer: microcode},
			{ID: "num_cpus", Text: "Number of CPUs:", Type: "text", Answer: strconv.Itoa(numCPUs)},
			{ID: "os_info", Text: "OS Information:", Type: "text", Answer: osInfo},
			{ID: "ram_info", Text: "Total RAM (GB):", Type: "text", Answer: fmt.Sprintf("%d", totalRAM)},
		},
		answers:      make(map[string]string),
		textInput:    ti,
		width:        120, // Set an initial width
		height:       30,  // Set an initial height
		listItems:    []string{"Option 1", "Option 2", "Option 3"},
		selectedItem: 0,
	}
	m.prepareNextQuestion()
	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			return m, tea.Quit
		case "enter":
			if m.confirmationMode {
				return m, tea.Quit
			}
			if m.currentIndex < len(m.questions) {
				switch m.questions[m.currentIndex].Type {
				case "select":
					return m.updateSelectQuestion(msg)
				case "yesno":
					return m.updateYesNoQuestion(msg)
				default:
					return m.updateTextQuestion(msg)
				}
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	if m.confirmationMode {
		return m.updateConfirmation(msg)
	}

	if m.currentIndex < len(m.questions) {
		switch m.questions[m.currentIndex].Type {
		case "select":
			return m.updateSelectQuestion(msg)
		case "yesno":
			return m.updateYesNoQuestion(msg)
		default:
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}
	}

	if m.currentIndex >= len(m.questions) {
		m.confirmationMode = true
		// Save answers to file
		if err := saveAnswersToFile(m.answers, "saved_answers.toml"); err != nil {
			m.errorMsg = fmt.Sprintf("Error saving answers: %v", err)
		}
		return m, nil
	}

	return m, nil
}

func (m *model) updateSelectQuestion(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "up":
			if m.selectedItem > 0 {
				m.selectedItem--
			}
		case "down":
			if m.selectedItem < len(m.listItems)-1 {
				m.selectedItem++
			}
		case "enter":
			fullAnswer := m.listItems[m.selectedItem]
			// Extract only the device name (e.g., /dev/sda) from the full answer
			deviceName := strings.Fields(fullAnswer)[0]
			m.questions[m.currentIndex].Answer = deviceName
			return m, m.nextQuestion()
		}
	}
	return m, nil
}

func (m *model) updateTextQuestion(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			currentAnswer := m.getCurrentAnswer()
			if m.questions[m.currentIndex].Validate != nil {
				if err := m.questions[m.currentIndex].Validate(currentAnswer, m.getAnswersMap()); err != nil {
					m.errorMsg = err.Error()
					if m.questions[m.currentIndex].ID == "confirm_password" {
						m.clearPasswordFields()
						for i, q := range m.questions {
							if q.ID == "password" {
								m.currentIndex = i
								m.prepareNextQuestion()
								break
							}
						}
					}
					return m, nil
				} else {
					m.errorMsg = ""
				}
			}
			m.questions[m.currentIndex].Answer = currentAnswer
			m.answers[m.questions[m.currentIndex].ID] = currentAnswer
			return m, m.nextQuestion()
		}
	}

	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func (m model) updateYesNoQuestion(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			m.questions[m.currentIndex].Answer = "yes"
			return m, m.nextQuestion()
		case "n", "N":
			m.questions[m.currentIndex].Answer = "no"
			return m, m.nextQuestion()
		}
	}
	return m, nil
}

func (m model) updateConfirmation(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y", "Y":
			if err := saveToFile(m.answers); err != nil {
				m.errorMsg = fmt.Sprintf("Error saving config: %v", err)
				return m, nil
			}
			return m, tea.Quit
		case "n", "N":
			m.confirmationMode = false
			m.currentIndex = 0
			m.prepareNextQuestion()
		case "q", "Q":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	// Define fixed widths for columns
	leftColumnWidth := 40
	rightColumnWidth := 60
	contentWidth := leftColumnWidth + rightColumnWidth + 3 // +3 for borders and space between columns

	// Define styles
	outerStyle := lipgloss.NewStyle().
		Padding(1).
		BorderStyle(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#5E81AC", Dark: "#88C0D0"}).
		Border(lipgloss.DoubleBorder(), true, true, true, true)

	titleStyle := lipgloss.NewStyle().
		Foreground(nord6).
		Background(nord3).
		Padding(0, 1).
		Bold(true).
		Width(contentWidth)

	titleText := "Installation Configuration"
	gradientTitle := ""
	for i, char := range titleText {
		color := lipgloss.Color(fmt.Sprintf("#{%02x}", 255-i*5))
		gradientTitle += lipgloss.NewStyle().Foreground(color).Render(string(char))
	}
	title := titleStyle.Render(gradientTitle)

	leftColumnStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(nord10).
		Width(leftColumnWidth).
		Foreground(nord4).
		Background(nord0).
		Padding(1, 0, 1, 1) // Add padding to top and bottom

	rightColumnStyle := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(nord10).
		Width(rightColumnWidth).
		Foreground(nord4)

	footerStyle := lipgloss.NewStyle().
		Foreground(nord4).
		Background(nord3).
		Padding(0, 1).
		Width(contentWidth).
		BorderTop(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(nord10)

	// Create left column content (variables)
	var leftContent string
	for _, q := range m.questions {
		value := m.answers[q.ID]
		if q.Type == "password" {
			value = strings.Repeat("*", len(value))
		}
		leftContent += fmt.Sprintf("🔹 %s: %s\n", q.ID, value)
	}
	leftColumn := leftColumnStyle.Render(leftContent)

	// Create right column content (current question)
	var rightContent string
	if m.currentIndex < len(m.questions) {
		q := m.questions[m.currentIndex]
		rightContent = fmt.Sprintf("%s\n\n", q.Text)
		switch q.Type {
		case "select":
			for i, item := range m.listItems {
				if i == m.selectedItem {
					rightContent += fmt.Sprintf("> %s\n", item)
				} else {
					rightContent += fmt.Sprintf("  %s\n", item)
				}
			}
		case "yesno":
			rightContent += "Press 'y' for Yes or 'n' for No"
		default:
			rightContent += m.textInput.View()
		}
	} else {
		rightContent = "Configuration complete. Press Enter to save and exit."
	}
	rightColumn := rightColumnStyle.Render(rightContent)

	// Create progress bar
	progressBarWidth := contentWidth - 10 // Subtract some padding
	progress := float64(m.currentIndex) / float64(len(m.questions))
	completedWidth := int(progress * float64(progressBarWidth))
	progressBar := lipgloss.NewStyle().
		Foreground(nord14).
		Render(strings.Repeat("▰", completedWidth)) +
		lipgloss.NewStyle().
			Foreground(nord3).
			Render(strings.Repeat("▱", progressBarWidth-completedWidth))
	progressText := fmt.Sprintf(" %d/%d ", m.currentIndex+1, len(m.questions))

	// Create footer with progress bar
	footer := footerStyle.Render(progressBar + progressText + "Press Ctrl+C to quit")

	// Combine all elements
	ui := lipgloss.JoinVertical(lipgloss.Left,
		title,
		lipgloss.JoinHorizontal(lipgloss.Top, leftColumn, rightColumn),
		footer,
	)

	// Add a subtle separator between the main content and the footer
	separator := lipgloss.NewStyle().
		Foreground(nord3).
		Render(strings.Repeat("─", contentWidth))

	// Add this line before rendering the footer
	ui = lipgloss.JoinVertical(lipgloss.Left, ui, separator)

	// Apply the outer style
	ui = outerStyle.Render(ui)

	// Center the UI in the terminal
	return lipgloss.Place(m.width, m.height,
		lipgloss.Center, lipgloss.Center,
		ui)
}

func (m model) confirmationView() string {
	content := "All questions have been answered.\n\n"
	content += "Are you sure you want to save these answers?\n"
	content += "Press 'y' to save and exit, 'n' to edit, or 'q' to quit without saving."

	return lipgloss.NewStyle().
		Width(m.width).
		Align(lipgloss.Left).
		Render(content)
}

func validateUsername(username string, _ map[string]string) error {
	if len(username) < 3 {
		return fmt.Errorf("username must be at least 3 characters long")
	}
	return nil
}

func validatePassword(password string, _ map[string]string) error {
	if len(password) < 8 {
		return fmt.Errorf("password must be at least 8 characters long")
	}
	return nil
}

func validateConfirmPassword(confirmPassword string, answers map[string]string) error {
	if confirmPassword != answers["password"] {
		return fmt.Errorf("passwords do not match")
	}
	return nil
}

func validateHostname(hostname string, _ map[string]string) error {
	if len(hostname) < 1 {
		return fmt.Errorf("hostname cannot be empty")
	}
	return nil
}

func saveToFile(answers map[string]string) error {
	file, err := os.Create("arch_config.toml")
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := toml.NewEncoder(file)
	return encoder.Encode(answers)
}

func (m *model) nextQuestion() tea.Cmd {
	m.answers[m.questions[m.currentIndex].ID] = m.questions[m.currentIndex].Answer
	m.currentIndex++
	if m.currentIndex >= len(m.questions) {
		m.confirmationMode = true
		return nil
	}
	m.prepareNextQuestion()
	return nil
}

func (m model) getAnswersMap() map[string]string {
	answers := make(map[string]string)
	for _, q := range m.questions {
		answers[q.ID] = q.Answer
	}
	return answers
}

func (m model) getCurrentAnswer() string {
	switch m.questions[m.currentIndex].Type {
	case "text", "password":
		return m.textInput.Value()
	case "select":
		return m.listItems[m.selectedItem]
	case "yesno":
		return m.questions[m.currentIndex].Answer
	default:
		return ""
	}
}

func (m *model) prepareNextQuestion() {
	if m.currentIndex >= len(m.questions) {
		m.confirmationMode = true
		return
	}

	question := m.questions[m.currentIndex]
	switch question.Type {
	case "select":
		m.listItems = question.Options
		m.selectedItem = 0
		// Set the selected item to the previously answered one, if it exists
		for i, option := range question.Options {
			if option == question.Answer {
				m.selectedItem = i
				break
			}
		}
	case "yesno":
		// No special preparation needed for yes/no questions
	default:
		// Set the text input value to the previous answer
		m.textInput.SetValue(question.Answer)
		m.textInput.Focus()
		if question.Type == "password" {
			m.textInput.EchoMode = textinput.EchoPassword
			m.textInput.EchoCharacter = '•'
		} else {
			m.textInput.EchoMode = textinput.EchoNormal
		}
	}
}

func (m *model) clearPasswordFields() {
	for i, q := range m.questions {
		if q.ID == "password" || q.ID == "confirm_password" {
			m.questions[i].Answer = ""
			delete(m.answers, q.ID)
		}
	}
}

func saveAnswersToFile(answers map[string]string, filename string) error {
	config := struct {
		Install struct {
			AutoRun bool `toml:"auto_run"`
		} `toml:"install"`
		Variables map[string]string `toml:"variables"`
	}{
		Install: struct {
			AutoRun bool `toml:"auto_run"`
		}{
			AutoRun: false,
		},
		Variables: answers,
	}

	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer file.Close()

	encoder := toml.NewEncoder(file)
	return encoder.Encode(config)
}

func loadAnswersFromFile(filename string) (map[string]string, error) {
	var config struct {
		Install struct {
			AutoRun bool `toml:"auto_run"`
		} `toml:"install"`
		Variables map[string]string `toml:"variables"`
	}

	_, err := toml.DecodeFile(filename, &config)
	if err != nil {
		return nil, err
	}

	return config.Variables, nil
}

func loadModelFromAnswers(answers map[string]string) model {
	m := initialModel()
	for i, q := range m.questions {
		if answer, ok := answers[q.ID]; ok {
			m.questions[i].Answer = answer
			m.answers[q.ID] = answer
		}
	}
	return m
}

func main() {
	fmt.Println("Welcome to the installation configuration!")

	var initialModelInstance model

	// Check if a saved file exists
	if _, err := os.Stat("arch_config.toml"); err == nil {
		// File exists, load the answers
		loadedAnswers, err := loadAnswersFromFile("arch_config.toml")
		if err != nil {
			fmt.Printf("Error loading saved answers: %v\n", err)
			initialModelInstance = initialModel()
		} else {
			initialModelInstance = loadModelFromAnswers(loadedAnswers)
		}
	} else {
		// File doesn't exist, start with a new model
		initialModelInstance = initialModel()
	}

	p := tea.NewProgram(initialModelInstance, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Printf("Error: %v", err)
		os.Exit(1)
	}

	if m, ok := finalModel.(model); ok {
		err := saveAnswersToFile(m.answers, "arch_config.toml")
		if err != nil {
			fmt.Printf("Error saving answers: %v\n", err)
		} else {
			fmt.Println("Configuration saved successfully.")
		}

		// Print the summary
		printSummary(m)
	}

	fmt.Print("Do you want to run the install script? (y/n): ")
	var response string
	fmt.Scanln(&response)

	if strings.ToLower(response) == "y" {
		runInstallScript(initialModelInstance.answers)
	} else {
		saveConfigWithoutInstall(initialModelInstance.answers)
	}

	fmt.Println("Configuration complete. Goodbye!")
}

func runInstallScript(answers map[string]string) {
	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "arch_install")
	if err != nil {
		fmt.Printf("Error creating temp directory: %v\n", err)
		return
	}
	defer os.RemoveAll(tempDir)

	// Extract all embedded files to the temp directory
	err = extractEmbeddedFiles(installFiles, "install", tempDir)
	if err != nil {
		fmt.Printf("Error extracting files: %v\n", err)
		return
	}

	// Run the install.sh script
	cmd := exec.Command("/bin/bash", filepath.Join(tempDir, "install.sh"))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), formatEnvVars(answers)...)
	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running install script: %v\n", err)
	}
}

func extractEmbeddedFiles(fsys embed.FS, root, destPath string) error {
	return fs.WalkDir(fsys, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		outPath := filepath.Join(destPath, strings.TrimPrefix(path, root))
		if d.IsDir() {
			return os.MkdirAll(outPath, os.ModePerm)
		}

		data, err := fsys.ReadFile(path)
		if err != nil {
			return err
		}

		return os.WriteFile(outPath, data, 0644)
	})
}

func formatEnvVars(answers map[string]string) []string {
	var env []string
	for k, v := range answers {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

func saveConfigWithoutInstall(answers map[string]string) {
	config := map[string]interface{}{
		"install":   map[string]bool{"auto_run": false},
		"variables": answers,
	}
	saveConfig(config, "Saving configuration without running install script...")
}

func saveConfig(config map[string]interface{}, message string) {
	fmt.Println(message)
	f, err := os.Create("arch_config.toml")
	if err != nil {
		fmt.Printf("Error creating config file: %v\n", err)
		return
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(config); err != nil {
		fmt.Printf("Error encoding TOML: %v\n", err)
	}
}

func printSummary(m model) {
	fmt.Println("\nConfiguration Summary:")
	fmt.Println("User Settings:")
	printSetting := func(name, key string) {
		if value, ok := m.answers[key]; ok {
			fmt.Printf("  %s: %s\n", name, value)
		}
	}

	printSetting("Username", "username")
	printSetting("Hostname", "hostname")
	printSetting("Timezone", "timezone")
	printSetting("Locale", "locale")
	printSetting("Keymap", "keymap")
	printSetting("Filesystem", "filesystem")
	printSetting("Desktop Environment", "desktop_env")
	printSetting("Disk Encryption", "disk_encryption")

	fmt.Println("\nSystem Information:")
	printSetting("CPU", "cpu_type")
	printSetting("CPU Cores", "num_cpus")
	printSetting("RAM", "ram_info")
	printSetting("Install Drive", "install_drive")
}
