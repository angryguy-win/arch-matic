package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"embed"

	"flag"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

//go:embed install
var installFiles embed.FS

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

func main() {
	// Define flags
	dryRun := flag.Bool("d", false, "Run in dry-run mode")
	flag.BoolVar(dryRun, "dry-run", false, "Run in dry-run mode")
	verbose := flag.Bool("v", false, "Run in verbose mode")
	flag.BoolVar(verbose, "verbose", false, "Run in verbose mode")
	flag.Parse()

	questions := loadQuestions()

	// Load default answers from arch_config.toml if it exists
	defaultAnswers, err := loadTOMLConfig("arch_config.toml")
	if err != nil && !os.IsNotExist(err) {
		fmt.Printf("Error loading arch_config.toml: %v\n", err)
	}

	initialModel := model{
		questions:    questions,
		currentIndex: 0,
		answers:      defaultAnswers, // Use loaded answers as defaults
		textInput:    textinput.New(),
	}
	initialModel.textInput.Focus()

	p := tea.NewProgram(initialModel)
	m, err := p.Run()
	if err != nil {
		fmt.Printf("Error running program: %v", err)
		os.Exit(1)
	}

	finalModel := m.(model)
	printSummary(finalModel)

	config := map[string]interface{}{
		"install":   map[string]bool{"auto_run": finalModel.answers["run_install"] == "true"},
		"variables": finalModel.answers,
	}

	if err := saveAndVerifyConfig(config); err != nil {
		fmt.Printf("Error saving configuration: %v\n", err)
		os.Exit(1)
	}

	if finalModel.answers["run_install"] == "true" {
		runInstallScript(finalModel.answers, *dryRun, *verbose)
	}
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
		cmd := exec.Command("bash", "-c", "grep -m1 'microcode' /proc/cpuinfo | awk '{print $3}'")
		output, err := cmd.Output()
		if err == nil {
			microcode = strings.TrimSpace(string(output))
		}
	}
	return
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
			m.answers[m.questions[m.currentIndex].ID] = deviceName

			// If the current question is INSTALL_DEVICE, update related fields
			if m.questions[m.currentIndex].ID == "INSTALL_DEVICE" {
				suffix := getPartitionSuffix(deviceName)
				m.answers["DEVICE"] = deviceName
				m.answers["PARTITION_BIOSBOOT"] = deviceName
				m.answers["PARTITION_EFI"] = fmt.Sprintf("%s%s2", deviceName, suffix)
				m.answers["PARTITION_ROOT"] = fmt.Sprintf("%s%s3", deviceName, suffix)
				m.answers["PARTITION_HOME"] = fmt.Sprintf("%s%s4", deviceName, suffix)
				m.answers["PARTITION_SWAP"] = fmt.Sprintf("%s%s5", deviceName, suffix)

				// Set mount options based on device type
				if strings.Contains(deviceName, "nvme") || strings.Contains(deviceName, "ssd") {
					m.answers["MOUNT_OPTIONS"] = "noatime,compress=zstd,ssd,commit=120"
				} else {
					m.answers["MOUNT_OPTIONS"] = "noatime,compress=zstd,nossd,commit=120"
				}

				// Update the answers in the questions array
				for i, q := range m.questions {
					if answer, ok := m.answers[q.ID]; ok {
						m.questions[i].Answer = answer
					}
				}
			}

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
				if err := m.questions[m.currentIndex].Validate(currentAnswer, m.answers); err != nil {
					m.errorMsg = err.Error()
					if m.questions[m.currentIndex].ID == "CONFIRM_PASSWORD" {
						m.clearPasswordFields()
						m.currentIndex = m.findQuestionIndex("PASSWORD")
						m.prepareNextQuestion()
						return m, nil
					}
					return m, nil
				}
				m.errorMsg = ""
			}
			m.questions[m.currentIndex].Answer = currentAnswer
			m.answers[m.questions[m.currentIndex].ID] = currentAnswer

			if m.questions[m.currentIndex].ID == "CONFIRM_PASSWORD" {
				delete(m.answers, "CONFIRM_PASSWORD") // Remove confirm password from final answers
			}

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
			m.questions[m.currentIndex].Answer = "true"
			m.answers[m.questions[m.currentIndex].ID] = "true"
			return m, m.nextQuestion()
		case "n", "N":
			m.questions[m.currentIndex].Answer = "false"
			m.answers[m.questions[m.currentIndex].ID] = "false"
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
	if confirmPassword != answers["PASSWORD"] {
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
		if q.ID == "PASSWORD" || q.ID == "CONFIRM_PASSWORD" {
			m.questions[i].Answer = ""
			delete(m.answers, q.ID)
		}
	}
	m.textInput.SetValue("")
}

func saveAnswersToFile(answers map[string]string, filename string) error {
	// Prepare the configuration structure
	config := struct {
		Install struct {
			AutoRun bool `toml:"auto_run"`
		} `toml:"install"`
		Variables map[string]string `toml:"variables"`
	}{
		Install: struct {
			AutoRun bool `toml:"auto_run"`
		}{
			AutoRun: answers["run_install"] == "true",
		},
		Variables: answers,
	}

	// Ensure the directory exists
	dir := filepath.Dir(filename)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return fmt.Errorf("error creating directory: %v", err)
	}

	// Save the configuration file
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating config file: %v", err)
	}
	defer file.Close()

	encoder := toml.NewEncoder(file)
	encoder.Indent = "  "
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("error encoding TOML: %v", err)
	}

	fmt.Printf("Configuration saved to %s\n", filename)
	return nil
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
	m := model{
		questions:    loadQuestions(),
		currentIndex: 0,
		answers:      make(map[string]string),
		textInput:    textinput.New(),
	}
	for i, q := range m.questions {
		if answer, ok := answers[q.ID]; ok {
			m.questions[i].Answer = answer
			m.answers[q.ID] = answer
		}
	}
	return m
}

func loadQuestions() []Question {
	driveOptions := getDriveInfo()
	timezone := []string{"UTC", "America/New_York", "America/Toronto", "America/Vancouver", "America/Chicago", "America/Denver",
		"America/Los_Angeles", "America/Texas", "Europe/London", "Europe/Berlin", "Asia/Tokyo"}
	locale := []string{"en_US.UTF-8", "de_DE.UTF-8", "fr_FR.UTF-8"}
	keymap := []string{"us", "uk", "de"}
	filesystem := []string{"btrfs", "ext4"}
	desktop_env := []string{"none", "gnome", "kde", "cosmic", "dwm"}

	// Your existing questions slice
	questions := []Question{
		{ID: "COUNTRY_ISO", Text: "Enter country ISO code:", Type: "text", Answer: "CA"},
		{ID: "INSTALL_DEVICE", Text: "Select installation device:", Type: "select", Options: driveOptions},
		{ID: "DEVICE", Text: "Confirm device path:", Type: "text"},
		{ID: "PARTITION_BIOSBOOT", Text: "Confirm BIOS boot partition:", Type: "text"},
		{ID: "PARTITION_EFI", Text: "Confirm EFI partition:", Type: "text"},
		{ID: "PARTITION_ROOT", Text: "Confirm root partition:", Type: "text"},
		{ID: "PARTITION_HOME", Text: "Confirm home partition:", Type: "text"},
		{ID: "PARTITION_SWAP", Text: "Confirm swap partition:", Type: "text"},
		{ID: "MOUNT_OPTIONS", Text: "Enter mount options:", Type: "text", Answer: "noatime,compress=zstd,ssd,commit=120"},
		{ID: "LOCALE", Text: "Select locale:", Type: "select", Options: locale},
		{ID: "TIMEZONE", Text: "Select timezone:", Type: "select", Options: timezone},
		{ID: "KEYMAP", Text: "Select keymap:", Type: "select", Options: keymap},
		{ID: "USERNAME", Text: "Enter username:", Type: "text", Validate: validateUsername},
		{ID: "PASSWORD", Text: "Enter password:", Type: "password", Validate: validatePassword},
		{ID: "CONFIRM_PASSWORD", Text: "Confirm password:", Type: "password", Validate: validateConfirmPassword},
		{ID: "HOSTNAME", Text: "Enter hostname:", Type: "text", Validate: validateHostname},
		{ID: "MICROCODE", Text: "Select microcode:", Type: "select", Options: []string{"amd", "intel"}},
		{ID: "GPU", Text: "Select GPU type:", Type: "select", Options: []string{"amd", "intel", "nvidia"}},
		{ID: "GPU_DRIVER", Text: "Select GPU driver:", Type: "select", Options: []string{"nvidia", "amdgpu", "intel"}},
		{ID: "TERMINAL", Text: "Select terminal:", Type: "select", Options: []string{"alacritty", "kitty"}},
		{ID: "SHELL", Text: "Select shell:", Type: "select", Options: []string{"bash", "zsh"}},
		{ID: "EDITOR", Text: "Select editor:", Type: "select", Options: []string{"nvim", "vim", "nano"}},
		{ID: "DESKTOP_ENVIRONMENT", Text: "Select desktop environment:", Type: "select", Options: desktop_env},
		{ID: "FORMAT_TYPE", Text: "Select filesystem format:", Type: "select", Options: filesystem},
		{ID: "SUBVOLUMES", Text: "Enter subvolumes (comma-separated):", Type: "text", Answer: "@,@home,@var,@.snapshots"},
		{ID: "LUKS_PASSWORD", Text: "Enter LUKS password (leave empty if not using):", Type: "password"},
		{ID: "LUKS", Text: "Use disk encryption?", Type: "yesno"},
		// Add these new questions at the end
		{
			ID:   "run_install",
			Text: "Do you want to run the install script?",
			Type: "yesno",
		},
	}

	// Load default answers
	defaultAnswers, _ := loadTOMLConfig("arch_config.toml")

	// Set default answers for questions if they exist in the config
	for i, q := range questions {
		if defaultValue, exists := defaultAnswers[q.ID]; exists {
			questions[i].Answer = defaultValue
		}
	}

	return questions
}

func getInstallOptions() (bool, bool, bool) {
	runInstall := confirmAction("Do you want to run the install script?")
	if !runInstall {
		return false, false, false
	}
	dryRun := confirmAction("Do you want to run the install script in dry run mode?")
	verbose := confirmAction("Do you want to run the install script in verbose mode?")
	return true, dryRun, verbose
}

func runInstallScript(answers map[string]string, dryRun bool, verbose bool) {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("Error getting current working directory: %v\n", err)
		return
	}

	archDir := filepath.Join(cwd, "install")
	scriptsDir := filepath.Join(archDir, "scripts")
	configFile := filepath.Join(archDir, "arch_config.toml")

	// **First**, extract embedded files
	err = extractEmbeddedFiles(installFiles, "install", archDir)
	if err != nil {
		fmt.Printf("Error extracting files: %v\n", err)
		return
	}

	// **Then**, save the updated configuration
	if err := saveAnswersToFile(answers, configFile); err != nil {
		fmt.Printf("Error saving config file: %v\n", err)
		return
	}

	// Proceed with running the install script
	installScriptPath := filepath.Join(archDir, "install.sh")
	args := []string{installScriptPath}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if verbose {
		args = append(args, "--verbose")
	}

	cmd := exec.Command("/bin/bash", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("ARCH_DIR=%s", archDir),
		fmt.Sprintf("SCRIPTS_DIR=%s", scriptsDir),
		fmt.Sprintf("CONFIG_FILE=%s", configFile),
	)

	fmt.Println("Running install script with the following options:")
	fmt.Printf("Dry Run: %v\n", dryRun)
	fmt.Printf("Verbose: %v\n", verbose)
	fmt.Println("Press Enter to continue or Ctrl+C to cancel...")
	bufio.NewReader(os.Stdin).ReadBytes('\n')

	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running install script: %v\n", err)
	}
}

func confirmAction(prompt string) bool {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Printf("%s (y/n): ", prompt)
		response, _ := reader.ReadString('\n')
		response = strings.ToLower(strings.TrimSpace(response))
		if response == "y" || response == "yes" {
			return true
		} else if response == "n" || response == "no" {
			return false
		}
		fmt.Println("Please answer with 'y' or 'n'")
	}
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("error opening source file: %v", err)
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("error creating destination file: %v", err)
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return fmt.Errorf("error copying file contents: %v", err)
	}

	err = destFile.Sync()
	if err != nil {
		return fmt.Errorf("error syncing destination file: %v", err)
	}

	return nil
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

func saveConfigAndRun(answers map[string]string, dryRun bool, verbose bool) {
	// Set auto_run to true
	config := map[string]interface{}{
		"install":   map[string]bool{"auto_run": true},
		"variables": answers,
	}

	if err := saveAndVerifyConfig(config); err != nil {
		fmt.Printf("Error saving configuration: %v\n", err)
		return
	}

	// Run the install script
	runInstallScript(answers, dryRun, verbose)
}

func saveConfigWithoutInstall(answers map[string]string) {
	config := map[string]interface{}{
		"install":   map[string]bool{"auto_run": false},
		"variables": answers,
	}

	if err := saveAndVerifyConfig(config); err != nil {
		fmt.Printf("Error saving configuration: %v\n", err)
	}
}

func saveAndVerifyConfig(config map[string]interface{}) error {
	rootPath := "arch_config.toml"
	installPath := filepath.Join("install", "arch_config.toml")

	// Save to root location
	if err := saveConfig(config, "Saving configuration to root...", rootPath); err != nil {
		return fmt.Errorf("error saving to root: %v", err)
	}

	// Ensure the install directory exists
	installDir := filepath.Dir(installPath)
	if err := os.MkdirAll(installDir, os.ModePerm); err != nil {
		return fmt.Errorf("error creating install directory: %v", err)
	}

	// Explicitly copy the file
	if err := copyFile(rootPath, installPath); err != nil {
		return fmt.Errorf("error copying config file: %v", err)
	}

	fmt.Printf("Configuration copied to %s\n", installPath)

	// Verify that both files are identical
	if err := verifyFiles(rootPath, installPath); err != nil {
		return fmt.Errorf("verification error: %v", err)
	}

	fmt.Println("Configuration saved and verified in both locations.")
	displayFileContents(rootPath, installPath)

	return nil
}

func saveConfig(config map[string]interface{}, message string, filePath string) error {
	fmt.Println(message)

	// Ensure the directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, os.ModePerm); err != nil {
		return fmt.Errorf("error creating directory: %v", err)
	}

	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating config file: %v", err)
	}
	defer f.Close()

	// Create the correct structure for the TOML file
	tomlConfig := struct {
		Install struct {
			AutoRun bool `toml:"auto_run"`
		} `toml:"install"`
		Variables map[string]string `toml:"variables"`
	}{
		Install: struct {
			AutoRun bool `toml:"auto_run"`
		}{
			AutoRun: config["install"].(map[string]bool)["auto_run"],
		},
		Variables: config["variables"].(map[string]string),
	}

	encoder := toml.NewEncoder(f)
	encoder.Indent = "  "
	if err := encoder.Encode(tomlConfig); err != nil {
		return fmt.Errorf("error encoding TOML: %v", err)
	}

	fmt.Printf("Configuration saved to %s\n", filePath)
	return nil
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
}

func getPartitionSuffix(device string) string {
	if strings.Contains(device, "nvme") || strings.Contains(device, "ssd") {
		return "p"
	}
	return ""
}

func (m *model) findQuestionIndex(id string) int {
	for i, q := range m.questions {
		if q.ID == id {
			return i
		}
	}
	return -1
}

func loadTOMLConfig(filename string) (map[string]string, error) {
	var config struct {
		Variables map[string]string `toml:"variables"`
	}
	_, err := toml.DecodeFile(filename, &config)
	if err != nil {
		return nil, err
	}
	return config.Variables, nil
}

func verifyFiles(file1, file2 string) error {
	content1, err := ioutil.ReadFile(file1)
	if err != nil {
		return fmt.Errorf("error reading %s: %v", file1, err)
	}

	content2, err := ioutil.ReadFile(file2)
	if err != nil {
		return fmt.Errorf("error reading %s: %v", file2, err)
	}

	if !bytes.Equal(content1, content2) {
		return fmt.Errorf("configuration files are not identical")
	}

	return nil
}

func displayFileContents(file1, file2 string) {
	content1, err := ioutil.ReadFile(file1)
	if err != nil {
		fmt.Printf("Error reading %s: %v\n", file1, err)
	} else {
		fmt.Printf("Contents of %s:\n%s\n\n", file1, string(content1))
	}

	content2, err := ioutil.ReadFile(file2)
	if err != nil {
		fmt.Printf("Error reading %s: %v\n", file2, err)
	} else {
		fmt.Printf("Contents of %s:\n%s\n\n", file2, string(content2))
	}
}
