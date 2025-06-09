package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"plandex-cli/api"
	"plandex-cli/auth"
	"plandex-cli/fs"
	"plandex-cli/lib"
	"plandex-cli/schema"
	"plandex-cli/term"
	"strconv"

	shared "plandex-shared"

	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var customModelsOnly bool
var allProperties bool
var saveCustomModels bool
var customModelsPath string

func init() {
	RootCmd.AddCommand(modelsCmd)

	modelsCmd.Flags().BoolVarP(&allProperties, "all", "a", false, "Show all properties")

	modelsCmd.AddCommand(listAvailableModelsCmd)
	modelsCmd.AddCommand(addCustomModelCmd)
	modelsCmd.AddCommand(updateCustomModelCmd)
	modelsCmd.AddCommand(manageCustomModelsCmd)
	modelsCmd.AddCommand(deleteCustomModelCmd)
	modelsCmd.AddCommand(defaultModelsCmd)

	manageCustomModelsCmd.Flags().BoolVar(&saveCustomModels, "save", false, "Save custom models")
	manageCustomModelsCmd.Flags().StringVarP(&customModelsPath, "file", "f", "", "Path to custom models file")

	listAvailableModelsCmd.Flags().BoolVarP(&customModelsOnly, "custom", "c", false, "List custom models only")
}

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "Show plan model settings",
	Run:   models,
}

var defaultModelsCmd = &cobra.Command{
	Use:   "default",
	Short: "Show default model settings for new plans",
	Run:   defaultModels,
}

var listAvailableModelsCmd = &cobra.Command{
	Use:     "available",
	Aliases: []string{"avail"},
	Short:   "List all available models",
	Run:     listAvailableModels,
}

var manageCustomModelsCmd = &cobra.Command{
	Use:   "custom",
	Short: "Manage custom models, providers, and model packs",
	Run:   manageCustomModels,
}

var addCustomModelCmd = &cobra.Command{
	Use:     "add",
	Aliases: []string{"create"},
	Short:   "Add a custom model",
	Run:     customModelsNotImplemented,
}

var updateCustomModelCmd = &cobra.Command{
	Use:     "update",
	Aliases: []string{"edit"},
	Short:   "Update a custom model",
	Run:     customModelsNotImplemented,
}

var deleteCustomModelCmd = &cobra.Command{
	Use:     "rm",
	Aliases: []string{"remove", "delete"},
	Short:   "Remove a custom model",
	Args:    cobra.MaximumNArgs(1),
	Run:     customModelsNotImplemented,
}

func manageCustomModels(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()

	term.StartSpinner("")

	errCh := make(chan *shared.ApiError, 3)
	var (
		customModels     []*shared.CustomModel
		customProviders  []*shared.CustomProvider
		customModelPacks []*shared.ModelPackSchema
	)

	go func() {
		models, apiErr := api.Client.ListCustomModels()
		if apiErr != nil {
			errCh <- apiErr
			return
		}
		customModels = models
		errCh <- nil
	}()

	go func() {
		// custom providers are not supported on cloud
		if auth.Current.IsCloud {
			errCh <- nil
			return
		}
		providers, apiErr := api.Client.ListCustomProviders()
		if apiErr != nil {
			errCh <- apiErr
			return
		}
		customProviders = providers
		errCh <- nil
	}()

	go func() {
		modelPacks, apiErr := api.Client.ListModelPacks()
		if apiErr != nil {
			errCh <- apiErr
			return
		}

		schemas := make([]*shared.ModelPackSchema, len(modelPacks))
		for i, modelPack := range modelPacks {
			schemas[i] = modelPack.ToModelPackSchema()
		}

		customModelPacks = schemas
		errCh <- nil
	}()

	for i := 0; i < 3; i++ {
		err := <-errCh
		if err != nil {
			term.OutputErrorAndExit("Error fetching custom models: %v", err.Msg)
			return
		}
	}

	existsById := map[string]bool{}
	for _, model := range customModels {
		existsById[string(model.ModelId)] = true
	}
	for _, provider := range customProviders {
		existsById[provider.Name] = true
	}
	for _, modelPack := range customModelPacks {
		existsById[modelPack.Name] = true
	}

	serverModelsInput := &shared.ModelsInput{
		CustomModels:     customModels,
		CustomProviders:  customProviders,
		CustomModelPacks: customModelPacks,
	}

	usingDefaultPath := false
	if customModelsPath == "" {
		usingDefaultPath = true
		customModelsPath = filepath.Join(fs.HomePlandexDir, "custom-models.json")
	}
	hashPath := customModelsPath + ".hash"

	cmdPrefix := "\\"
	if !term.IsRepl {
		cmdPrefix = "plandex "
	}

	exists := false
	_, err := os.Stat(customModelsPath)
	if err == nil {
		exists = true
	} else if os.IsNotExist(err) {
		exists = false
	} else {
		term.OutputErrorAndExit("Error checking custom models file: %v", err)
		return
	}

	var jsonData []byte

	if saveCustomModels {
		if !exists {
			term.OutputErrorAndExit("File not found: %s", customModelsPath)
			return
		}
		jsonData, err = os.ReadFile(customModelsPath)
		if err != nil {
			term.OutputErrorAndExit("Error reading JSON file: %v", err)
			return
		}
	} else {
		if serverModelsInput.IsEmpty() {
			jsonData, err = json.MarshalIndent(getExampleTemplate(auth.Current.IsCloud), "", "  ")
			if err != nil {
				term.OutputErrorAndExit("Error marshalling template: %v", err)
				return
			}

			err = os.MkdirAll(filepath.Dir(customModelsPath), 0755)
			if err != nil {
				term.OutputErrorAndExit("Error creating directory: %v", err)
				return
			}

			err = os.WriteFile(customModelsPath, jsonData, 0644)
			if err != nil {
				term.OutputErrorAndExit("Error writing template file: %v", err)
				return
			}

			term.StopSpinner()

			fmt.Printf("🧠 Example models file → %s\n", customModelsPath)
			fmt.Println("👨‍💻 Edit it, then come back here to save")
			fmt.Println()
		} else {
			localJsonData, err := os.ReadFile(customModelsPath)
			if err != nil {
				term.OutputErrorAndExit("Error reading JSON file: %v", err)
				return
			}

			var localModelsInput shared.ModelsInput
			err = json.Unmarshal(localJsonData, &localModelsInput)
			if err != nil {
				term.OutputErrorAndExit("Error unmarshalling JSON file: %v", err)
				return
			}

			// we only do a conflict check on the default path in the home dir
			// if user specifies the path and the file exists, just open the file without checking for conflicts
			var currentHash string
			if usingDefaultPath {
				lastSavedHash, err := os.ReadFile(hashPath)
				if err != nil && !os.IsNotExist(err) {
					term.OutputErrorAndExit("Error reading hash file: %v", err)
					return
				}

				currentHash, err = localModelsInput.Hash()
				if err != nil {
					term.OutputErrorAndExit("Error hashing models: %v", err)
					return
				}

				if currentHash != string(lastSavedHash) {
					term.StopSpinner()
					color.New(color.Bold, term.ColorHiYellow).Println("⚠️  The models file has local changes")

					fmt.Println()
					fmt.Println("Path → " + customModelsPath)
					fmt.Println()

					fmt.Println("If you continue, local changes will be dropped in favor of the latest server state")

					fmt.Println()
					fmt.Printf("To keep the local version instead, quit and run %s\n", color.New(color.Bold, color.BgCyan, color.FgHiWhite).
						Sprintf(" %smodels custom --save ", cmdPrefix))
					fmt.Println()
					res, err := term.ConfirmYesNo("Drop local changes and continue?")
					if err != nil {
						term.OutputErrorAndExit("Error confirming: %v", err)
						return
					}
					if !res {
						return
					}
					fmt.Println()
					term.StartSpinner("")
				}
			}

			serverModelsInput.PrepareUpdate()

			if serverModelsInput.Equals(localModelsInput) {
				jsonData = localJsonData
			} else {
				jsonData, err = json.MarshalIndent(serverModelsInput, "", "  ")
				if err != nil {
					term.OutputErrorAndExit("Error marshalling models: %v", err)
					return
				}

				err = os.WriteFile(customModelsPath, jsonData, 0644)
				if err != nil {
					term.OutputErrorAndExit("Error writing file: %v", err)
					return
				}
			}

			if currentHash != "" {
				err = os.WriteFile(hashPath, []byte(currentHash), 0644)
				if err != nil {
					term.OutputErrorAndExit("Error writing hash file: %v", err)
					return
				}
			}

			term.StopSpinner()

			fmt.Printf("🧠 %s → %s\n", color.New(color.Bold, term.ColorHiCyan).Sprint("Models file"), customModelsPath)
			fmt.Println("👨‍💻 Edit it, then come back here to save")
			fmt.Println()
		}

		pathArg := ""
		if !usingDefaultPath {
			pathArg = fmt.Sprintf(" --file %s", customModelsPath)
		}
		printManual := func() {
			fmt.Println("To save changes, run " +
				fmt.Sprintf(" %s ", color.New(color.Bold, color.BgCyan, color.FgHiWhite).
					Sprintf(" %smodels custom --save%s ", cmdPrefix, pathArg)))
		}

		selectedEditor := maybePromptAndOpen(customModelsPath)

		if selectedEditor {
			fmt.Println("📝 Opened in editor")
			fmt.Println()

			confirmed, err := term.ConfirmYesNo("Ready to save?")
			if err != nil {
				term.OutputErrorAndExit("Error confirming: %v", err)
				return
			}

			if !confirmed {
				fmt.Println("🙅‍♂️ Update canceled")
				fmt.Println()

				printManual()
				return
			}

			// get updated file state
			jsonData, err = os.ReadFile(customModelsPath)
			if err != nil {
				term.OutputErrorAndExit("Error reading JSON file: %v", err)
				return
			}
		} else {
			// No editor available or user chose manual
			fmt.Println("👨‍💻 Edit the file in your JSON editor of choice")
			fmt.Println()

			printManual()
			return
		}
	}

	term.StartSpinner("")

	modelsInput, err := schema.ValidateModelsInputJSON(jsonData)
	if err != nil {
		term.StopSpinner()
		color.New(color.Bold, term.ColorHiRed).Println("🚨 Error validating JSON file")
		fmt.Println(err.Error())
		return
	}

	noDuplicates, errMsg := modelsInput.CheckNoDuplicates()
	if !noDuplicates {
		term.StopSpinner()
		color.New(color.Bold, term.ColorHiRed).Println("🚨 Some items are duplicated:")
		fmt.Println()
		fmt.Println(errMsg)
		return
	}

	printNoChanges := func() {
		term.StopSpinner()
		fmt.Println("🤷‍♂️ No changes to custom models/providers/model packs")
	}

	if modelsInput.IsEmpty() {
		printNoChanges()
		return
	}

	apiErr := api.Client.CreateCustomModels(&modelsInput)

	if apiErr != nil {
		term.OutputErrorAndExit("Error importing models: %v", apiErr.Msg)
		return
	}

	// only write the hash if we're using the default path in the home dir
	// otherwise we don't do conflict checking, so we don't need the hash file
	if usingDefaultPath {
		hash, err := modelsInput.Hash()
		if err != nil {
			term.OutputErrorAndExit("Error hashing models: %v", err)
			return
		}

		err = os.WriteFile(hashPath, []byte(hash), 0644)
		if err != nil {
			term.OutputErrorAndExit("Error writing hash file: %v", err)
			return
		}
	}

	inputModelIds := map[string]bool{}
	inputProviderNames := map[string]bool{}
	inputModelPackNames := map[string]bool{}
	for _, model := range modelsInput.CustomModels {
		inputModelIds[string(model.ModelId)] = true
	}
	for _, provider := range modelsInput.CustomProviders {
		inputProviderNames[provider.Name] = true
	}
	for _, modelPack := range modelsInput.CustomModelPacks {
		inputModelPackNames[modelPack.Name] = true
	}

	updatedModelsInput := modelsInput.FilterUnchanged(&shared.ModelsInput{
		CustomModels:     customModels,
		CustomProviders:  customProviders,
		CustomModelPacks: customModelPacks,
	})

	term.StopSpinner()

	added := strings.Builder{}
	updated := strings.Builder{}
	deleted := strings.Builder{}

	for _, provider := range updatedModelsInput.CustomProviders {
		action := "✅ Added"
		builder := &added
		if existsById[provider.Name] {
			action = "🔄 Updated"
			builder = &updated
		}
		builder.WriteString(fmt.Sprintf("%s custom %s → %s\n",
			action,
			color.New(term.ColorHiCyan).Sprint("provider"),
			color.New(color.Bold, term.ColorHiGreen).Sprint(provider.Name)))
	}
	for _, provider := range customProviders {
		if !inputProviderNames[provider.Name] {
			deleted.WriteString(fmt.Sprintf("❌ Removed custom %s → %s\n",
				color.New(term.ColorHiCyan).Sprint("provider"),
				color.New(color.Bold, term.ColorHiRed).Sprint(provider.Name)))
		}
	}

	for _, model := range updatedModelsInput.CustomModels {
		action := "✅ Added"
		builder := &added
		if existsById[string(model.ModelId)] {
			action = "🔄 Updated"
			builder = &updated
		}
		builder.WriteString(fmt.Sprintf("%s custom %s → %s\n",
			action,
			color.New(term.ColorHiCyan).Sprint("model"),
			color.New(color.Bold, term.ColorHiGreen).Sprint(string(model.ModelId))))
	}
	for _, model := range customModels {
		if !inputModelIds[string(model.ModelId)] {
			deleted.WriteString(fmt.Sprintf("❌ Removed custom %s → %s\n",
				color.New(term.ColorHiCyan).Sprint("model"),
				color.New(color.Bold, term.ColorHiRed).Sprint(string(model.ModelId))))
		}
	}

	for _, modelPack := range updatedModelsInput.CustomModelPacks {
		action := "✅ Added"
		builder := &added
		if existsById[modelPack.Name] {
			action = "🔄 Updated"
			builder = &updated
		}
		builder.WriteString(fmt.Sprintf("%s custom %s → %s\n",
			action,
			color.New(term.ColorHiCyan).Sprint("model pack"),
			color.New(color.Bold, term.ColorHiGreen).Sprint(modelPack.Name)))
	}
	for _, modelPack := range customModelPacks {
		if !inputModelPackNames[modelPack.Name] {
			deleted.WriteString(fmt.Sprintf("❌ Removed custom %s → %s\n",
				color.New(term.ColorHiCyan).Sprint("model pack"),
				color.New(color.Bold, term.ColorHiRed).Sprint(modelPack.Name)))
		}
	}

	if updated.Len()+added.Len()+deleted.Len() == 0 {
		printNoChanges()
		return
	}

	fmt.Print(added.String())
	fmt.Print(updated.String())
	fmt.Print(deleted.String())
}

func models(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()
	lib.MustResolveProject()

	if lib.CurrentPlanId == "" {
		term.OutputNoCurrentPlanErrorAndExit()
	}

	term.StartSpinner("")

	plan, err := api.Client.GetPlan(lib.CurrentPlanId)

	if err != nil {
		term.StopSpinner()
		term.OutputErrorAndExit("Error getting plan: %v", err)
		return
	}

	settings, err := api.Client.GetSettings(lib.CurrentPlanId, lib.CurrentBranch)
	term.StopSpinner()

	if err != nil {
		term.OutputErrorAndExit("Error getting settings: %v", err)
		return
	}

	title := fmt.Sprintf("%s Model Settings", color.New(color.Bold, term.ColorHiGreen).Sprint(plan.Name))

	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.Append([]string{title})
	table.Render()
	fmt.Println()

	renderSettings(settings, allProperties)

	term.PrintCmds("", "set-model", "models available", "models default")
}

func defaultModels(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()

	term.StartSpinner("")
	settings, err := api.Client.GetOrgDefaultSettings()
	term.StopSpinner()

	if err != nil {
		term.OutputErrorAndExit("Error getting default model settings: %v", err)
		return
	}

	title := fmt.Sprintf("%s Model Settings", color.New(color.Bold, term.ColorHiGreen).Sprint("Org-Wide Default"))
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	table.Append([]string{title})
	table.Render()
	fmt.Println()

	renderSettings(settings, allProperties)

	term.PrintCmds("", "set-model default", "models available", "models")
}

func listAvailableModels(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()

	term.StartSpinner("")

	customModels, err := api.Client.ListCustomModels()

	term.StopSpinner()

	if err != nil {
		term.OutputErrorAndExit("Error fetching custom models: %v", err)
		return
	}

	if !customModelsOnly {
		color.New(color.Bold, term.ColorHiCyan).Println("🏠 Built-in Models")
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetHeader([]string{"Model", "Input", "Output", "Reserved"})
		for _, model := range shared.BuiltInBaseModels {
			table.Append([]string{string(model.ModelId), fmt.Sprintf("%d 🪙", model.MaxTokens), fmt.Sprintf("%d 🪙", model.MaxOutputTokens), fmt.Sprintf("%d 🪙", model.ReservedOutputTokens)})
		}
		table.Render()
		fmt.Println()
	}

	if len(customModels) > 0 {
		color.New(color.Bold, term.ColorHiCyan).Println("🛠️  Custom Models")
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetHeader([]string{"#", "ID", "🪙"})
		for i, model := range customModels {
			table.Append([]string{fmt.Sprintf("%d", i+1), string(model.ModelId), strconv.Itoa(model.MaxTokens)})
		}
		table.Render()
	} else if customModelsOnly {
		fmt.Println("🤷‍♂️ No custom models")
	}
	fmt.Println()

	if customModelsOnly {
		if len(customModels) > 0 {
			term.PrintCmds("", "models", "set-model", "models add", "models delete")
		} else {
			term.PrintCmds("", "models add")
		}
	} else {
		term.PrintCmds("", "models available --custom", "models", "set-model", "models add", "models delete")
	}
}

func renderSettings(settings *shared.PlanSettings, allProperties bool) {
	modelPack := settings.ModelPack

	color.New(color.Bold, term.ColorHiCyan).Println("🎛️  Current Model Pack")
	renderModelPack(modelPack, allProperties)

	if allProperties {
		color.New(color.Bold, term.ColorHiCyan).Println("🧠 Planner Defaults")
		table := tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetHeader([]string{"Max Tokens", "Max Convo Tokens"})
		table.Append([]string{
			fmt.Sprintf("%d", modelPack.Planner.GetFinalLargeContextFallback().GetSharedBaseConfig().MaxTokens),
			fmt.Sprintf("%d", modelPack.Planner.GetMaxConvoTokens()),
		})
		table.Render()
		fmt.Println()

		color.New(color.Bold, term.ColorHiCyan).Println("⚙️  Planner Overrides")
		table = tablewriter.NewWriter(os.Stdout)
		table.SetAutoWrapText(false)
		table.SetHeader([]string{"Name", "Value"})
		if settings.ModelOverrides.MaxTokens == nil {
			table.Append([]string{"Max Tokens", "no override"})
		} else {
			table.Append([]string{"Max Tokens", fmt.Sprintf("%d", *settings.ModelOverrides.MaxTokens)})
		}
		if settings.ModelOverrides.MaxConvoTokens == nil {
			table.Append([]string{"Max Convo Tokens", "no override"})
		} else {
			table.Append([]string{"Max Convo Tokens", fmt.Sprintf("%d", *settings.ModelOverrides.MaxConvoTokens)})
		}
		table.Render()
		fmt.Println()
	}
}

func renderModelPack(modelPack *shared.ModelPack, allProperties bool) {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetAutoFormatHeaders(false)
	table.SetAutoWrapText(true)
	table.SetColWidth(64)
	table.SetHeader([]string{modelPack.Name})
	table.Append([]string{modelPack.Description})
	table.Render()
	fmt.Println()

	color.New(color.Bold, term.ColorHiCyan).Println("🤖 Models")
	table = tablewriter.NewWriter(os.Stdout)
	table.SetAutoWrapText(false)
	cols := []string{
		"Role",
		"Model",
	}
	align := []int{
		tablewriter.ALIGN_LEFT, // Role
		tablewriter.ALIGN_LEFT, // Model
	}
	if allProperties {
		cols = append(cols, []string{
			"Temperature",
			"Top P",
			"Max Input",
		}...)
		align = append(align, []int{
			tablewriter.ALIGN_RIGHT, // Temperature
			tablewriter.ALIGN_RIGHT, // Top P
			tablewriter.ALIGN_RIGHT, // Max Input
		}...)
	}
	table.SetHeader(cols)
	table.SetColumnAlignment(align)

	anyRoleParamsDisabled := false

	var addModelRow func(role string, config shared.ModelRoleConfig, indent int)
	addModelRow = func(role string, config shared.ModelRoleConfig, indent int) {
		if indent > 0 {
			role = "└─ " + role
			for i := 0; i < indent-1; i++ {
				role = " " + role
			}
		}

		var temp float32
		var topP float32
		var disabled bool

		if config.GetSharedBaseConfig().RoleParamsDisabled {
			temp = 1
			topP = 1
			disabled = true
			anyRoleParamsDisabled = true
		} else {
			temp = config.Temperature
			topP = config.TopP
		}

		tempStr := fmt.Sprintf("%.1f", temp)
		if disabled {
			tempStr = "*" + tempStr
		}

		topPStr := fmt.Sprintf("%.1f", topP)
		if disabled {
			topPStr = "*" + topPStr
		}

		row := []string{
			role,
			string(config.GetModelId()),
		}

		if allProperties {
			row = append(row, []string{
				tempStr,
				topPStr,
				fmt.Sprintf("%d 🪙", config.GetSharedBaseConfig().MaxTokens-config.GetReservedOutputTokens()),
			}...)
		}
		table.Append(row)

		// Add large context and large output fallback(s) if present
		if config.LargeContextFallback != nil {
			addModelRow("large-context", *config.LargeContextFallback, indent+1)
		}

		if config.LargeOutputFallback != nil {
			addModelRow("large-output", *config.LargeOutputFallback, indent+1)
		}

		if config.StrongModel != nil {
			addModelRow("strong", *config.StrongModel, indent+1)
		}

		if config.ErrorFallback != nil {
			addModelRow("error", *config.ErrorFallback, indent+1)
		}
	}

	addModelRow(string(shared.ModelRolePlanner), modelPack.Planner.ModelRoleConfig, 0)

	addModelRow(string(shared.ModelRoleArchitect), modelPack.GetArchitect(), 0)
	addModelRow(string(shared.ModelRoleCoder), modelPack.GetCoder(), 0)
	addModelRow(string(shared.ModelRolePlanSummary), modelPack.PlanSummary, 0)
	addModelRow(string(shared.ModelRoleBuilder), modelPack.Builder, 0)
	addModelRow(string(shared.ModelRoleWholeFileBuilder), modelPack.GetWholeFileBuilder(), 0)
	addModelRow(string(shared.ModelRoleName), modelPack.Namer, 0)
	addModelRow(string(shared.ModelRoleCommitMsg), modelPack.CommitMsg, 0)
	addModelRow(string(shared.ModelRoleExecStatus), modelPack.ExecStatus, 0)
	table.Render()

	if anyRoleParamsDisabled && allProperties {
		fmt.Println("* these models do not support changing temperature or top p")
	}

	fmt.Println()

}

func customModelsNotImplemented(cmd *cobra.Command, args []string) {
	color.New(color.Bold, color.FgHiRed).Println("⛔️ Not implemented")
	fmt.Println()
	fmt.Println("Use " + color.New(color.BgCyan, color.FgHiWhite).Sprint(" plandex models custom ") + " to manage custom models, providers, and model packs")
	os.Exit(1)
}

type editorCandidate struct {
	name        string
	cmd         string
	args        []string
	isJetBrains bool
}

const maxEditorOpts = 5

func detectEditors() []editorCandidate {
	guess := []editorCandidate{
		// Popular non-JetBrains launchers
		{"VS Code", "code", nil, false},
		{"Cursor", "cursor", nil, false},
		{"Zed", "zed", nil, false},
		{"Neovim", "nvim", nil, false},

		// JetBrains IDE-specific launchers
		{"IntelliJ IDEA", "idea", nil, true},
		{"GoLand", "goland", nil, true},
		{"PyCharm", "pycharm", nil, true},
		{"CLion", "clion", nil, true},
		{"WebStorm", "webstorm", nil, true},
		{"PhpStorm", "phpstorm", nil, true},
		{"DataGrip", "datagrip", nil, true},
		{"RubyMine", "rubymine", nil, true},
		{"Rider", "rider", nil, true},
		{"DataSpell", "dataspell", nil, true},

		// JetBrains universal CLI (2023.2+)
		{"JetBrains (jb)", "jb", []string{"open"}, true},

		{"Vim", "vim", nil, false},
		{"Nano", "nano", nil, false},
		{"Helix", "hx", nil, false},
		{"Micro", "micro", nil, false},
		{"Sublime Text", "subl", nil, false},
		{"TextMate", "mate", nil, false},
		{"Kakoune", "kak", nil, false},
		{"Emacs", "emacs", nil, false},
		{"Kate", "kate", nil, false},
	}
	pref := map[string]bool{}
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := os.Getenv(env); v != "" {
			// keep only the binary name, drop path/flags
			cmd := filepath.Base(strings.Fields(v)[0])
			pref[cmd] = true
		}
	}

	_, err := exec.LookPath("jb") // true if universal launcher exists
	jbOnPath := err == nil

	var found []editorCandidate
	for _, c := range guess {
		if _, err := exec.LookPath(c.cmd); err != nil {
			continue // not on PATH
		}

		// If jb is present, drop per-IDE launchers *unless* this exact cmd
		// is marked preferred by VISUAL/EDITOR.
		if jbOnPath && c.isJetBrains && !pref[c.cmd] {
			continue
		}
		found = append(found, c)
	}

	for cmd := range pref {
		if _, err := exec.LookPath(cmd); err == nil {
			already := false
			for _, c := range found {
				if c.cmd == cmd {
					already = true
					break
				}
			}
			if !already {
				found = append(found, editorCandidate{name: cmd, cmd: cmd})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		pi, pj := pref[found[i].cmd], pref[found[j].cmd]
		if pi == pj {
			return false // keep original order
		}
		return pi // true → i comes before j
	})
	if len(found) > maxEditorOpts {
		found = found[:maxEditorOpts]
	}

	return found
}

func maybePromptAndOpen(path string) bool {
	editors := detectEditors()
	if len(editors) == 0 {
		// just exit if there are no editors available
		return false
	}
	opts := []string{}
	for _, c := range editors {
		opts = append(opts, "Open with "+c.name)
	}

	const openManually = "Open manually"
	opts = append(opts, openManually)

	choice, err := term.SelectFromList("Open the file now?", opts)
	if err != nil {
		term.OutputErrorAndExit("Error selecting editor: %v", err)
	}

	if choice == openManually {
		return false
	}

	var idx int
	for i, c := range opts {
		if c == choice {
			idx = i
			break
		}
	}

	if idx < len(editors) {
		sel := editors[idx]
		err = exec.Command(sel.cmd, append(sel.args, path)...).Start()
		if err != nil {
			term.OutputErrorAndExit("Error opening template: %v", err)
		}
		return true
	}

	return false
}

func getExampleTemplate(isCloud bool) shared.ModelsInput {
	exampleProviderName := "togetherai"

	var customProviders []*shared.CustomProvider
	usesProviders := []shared.BaseModelUsesProvider{}
	if !isCloud {
		customProviders = append(customProviders, &shared.CustomProvider{
			Name:         exampleProviderName,
			BaseUrl:      "https://api.together.xyz/v1",
			ApiKeyEnvVar: "TOGETHER_API_KEY",
		})

		usesProviders = append(usesProviders, shared.BaseModelUsesProvider{
			Provider:       shared.ModelProviderCustom,
			CustomProvider: &exampleProviderName,
			ModelName:      "meta-llama/Llama-4-Maverick-17B-128E-Instruct-FP8",
		})
	}
	usesProviders = append(usesProviders, shared.BaseModelUsesProvider{
		Provider:  shared.ModelProviderOpenRouter,
		ModelName: "meta-llama/llama-4-maverick",
	})

	return shared.ModelsInput{
		SchemaUrl:       shared.SchemaUrlInputConfig,
		CustomProviders: customProviders,
		CustomModels: []*shared.CustomModel{
			{
				ModelId:     shared.ModelId("meta-llama/llama-4-maverick"),
				Publisher:   shared.ModelPublisher("meta-llama"),
				Description: "Meta Llama 4 Maverick",

				BaseModelShared: shared.BaseModelShared{
					DefaultMaxConvoTokens: 75000,
					MaxTokens:             1048576,
					MaxOutputTokens:       16000,
					ReservedOutputTokens:  16000,
					ModelCompatibility:    shared.FullCompatibility,
					PreferredOutputFormat: shared.ModelOutputFormatXml,
				},

				Providers: usesProviders,
			},
		},
		CustomModelPacks: []*shared.ModelPackSchema{
			{
				Name:        "example-model-pack",
				Description: "Example model pack",
				Planner: shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("deepseek/r1"),
				},
				Architect: &shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("deepseek/r1"),
				},
				Coder: &shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("deepseek/v3-0324"),
				},
				PlanSummary: shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("meta-llama/llama-4-maverick"),
				},
				Builder: shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("deepseek/r1-hidden"),
				},
				WholeFileBuilder: &shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("deepseek/r1-hidden"),
				},
				ExecStatus: shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("deepseek/r1-hidden"),
				},
				Namer: shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("meta-llama/llama-4-maverick"),
				},
				CommitMsg: shared.ModelRoleConfigSchema{
					ModelId: shared.ModelId("meta-llama/llama-4-maverick"),
				},
			},
		},
	}
}
