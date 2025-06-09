package cmd

import (
	"encoding/json"
	"fmt"
	"plandex-cli/api"
	"plandex-cli/auth"
	"plandex-cli/lib"
	"plandex-cli/term"
	"reflect"
	"strconv"
	"strings"

	shared "plandex-shared"

	"github.com/spf13/cobra"
)

func init() {
	RootCmd.AddCommand(modelsSetCmd)

	modelsSetCmd.AddCommand(defaultModelSetCmd)

}

var modelsSetCmd = &cobra.Command{
	Use:     "set-model [model-set-or-role-or-setting] [property-or-value] [value]",
	Aliases: []string{"set-models"},
	Short:   "Update current plan model settings",
	Run:     modelsSet,
	Args:    cobra.MaximumNArgs(3),
}

var defaultModelSetCmd = &cobra.Command{
	Use:   "default [model-set-or-role-or-setting] [property-or-value] [value]",
	Short: "Update org-wide default model settings",
	Run:   defaultModelsSet,
	Args:  cobra.MaximumNArgs(3),
}

func modelsSet(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()
	lib.MustResolveProject()

	term.StartSpinner("")
	originalSettings, apiErr := api.Client.GetSettings(lib.CurrentPlanId, lib.CurrentBranch)
	term.StopSpinner()

	if apiErr != nil {
		term.OutputErrorAndExit("Error getting current settings: %v", apiErr)
		return
	}

	settings := updateModelSettings(args, originalSettings)

	if settings == nil {
		return
	}

	term.StartSpinner("")
	res, apiErr := api.Client.UpdateSettings(
		lib.CurrentPlanId,
		lib.CurrentBranch,
		shared.UpdateSettingsRequest{
			Settings: settings,
		})
	term.StopSpinner()

	if apiErr != nil {
		term.OutputErrorAndExit("Error updating settings: %v", apiErr)
		return
	}

	fmt.Println(res.Msg)
	fmt.Println()
	term.PrintCmds("", "models", "set-model default", "log")
}

func defaultModelsSet(cmd *cobra.Command, args []string) {
	auth.MustResolveAuthWithOrg()

	term.StartSpinner("")
	originalSettings, apiErr := api.Client.GetOrgDefaultSettings()
	term.StopSpinner()

	if apiErr != nil {
		term.OutputErrorAndExit("Error getting current settings: %v", apiErr)
		return
	}

	settings := updateModelSettings(args, originalSettings)

	if settings == nil {
		return
	}

	term.StartSpinner("")
	res, apiErr := api.Client.UpdateOrgDefaultSettings(
		shared.UpdateSettingsRequest{
			Settings: settings,
		})
	term.StopSpinner()

	if apiErr != nil {
		term.OutputErrorAndExit("Error updating settings: %v", apiErr)
		return
	}

	fmt.Println(res.Msg)
	fmt.Println()
	term.PrintCmds("", "models", "set-model default", "log")
}

func updateModelSettings(args []string, originalSettings *shared.PlanSettings) *shared.PlanSettings {
	// Marshal and unmarshal to make a deep copy of the settings
	jsonBytes, err := json.Marshal(originalSettings)
	if err != nil {
		term.OutputErrorAndExit("Error marshalling settings: %v", err)
		return nil
	}

	builtInModelPacks := shared.BuiltInModelPacks

	if auth.Current.IsCloud {
		filtered := []*shared.ModelPack{}
		for _, ms := range builtInModelPacks {
			if ms.LocalProvider == "" {
				filtered = append(filtered, ms)
			}
		}
		builtInModelPacks = filtered
	}

	var settings *shared.PlanSettings
	err = json.Unmarshal(jsonBytes, &settings)
	if err != nil {
		term.OutputErrorAndExit("Error unmarshalling settings: %v", err)
		return nil
	}

	var modelSetOrRoleOrSetting, propertyCompact, value string
	var modelPack *shared.ModelPack
	var role shared.ModelRole
	var settingCompact string
	var settingDasherized string
	var selectedModelId shared.ModelId
	var temperature *float64
	var topP *float64
	var reservedOutputTokens *int

	if len(args) > 0 {
		modelSetOrRoleOrSetting = args[0]

		compare := modelSetOrRoleOrSetting
		if compare == "daily" {
			compare = "daily-driver"
		}
		if compare == "opus-4-planner" {
			compare = "opus-planner"
		}

		for _, ms := range builtInModelPacks {
			if strings.EqualFold(ms.Name, compare) {
				modelPack = ms
				break
			}
		}

		if modelPack == nil {
			for _, r := range shared.AllModelRoles {
				if strings.EqualFold(string(r), modelSetOrRoleOrSetting) {
					role = r
					break
				}
			}
			if role == "" {
				for _, s := range shared.ModelOverridePropsDasherized {
					compact := shared.Compact(s)
					if strings.EqualFold(compact, shared.Compact(modelSetOrRoleOrSetting)) {
						settingCompact = compact
						settingDasherized = s
						break
					}
				}
			}
		}
	}

	if modelPack == nil && role == "" && settingCompact == "" {
		// Prompt user to select between updating a model-set, a top-level setting or a model role
		opts := []string{"🎛️  choose a model pack to change all roles at once"}

		for _, role := range shared.AllModelRoles {
			label := fmt.Sprintf("🤖 role | %s → %s", role, shared.ModelRoleDescriptions[role])
			opts = append(opts, label)
		}
		for _, setting := range shared.ModelOverridePropsDasherized {
			label := fmt.Sprintf("⚙️  override | %s → %s", shared.Dasherize(setting), shared.SettingDescriptions[setting])
			opts = append(opts, label)
		}

		selection, err := term.SelectFromList("Choose a new model pack, or select a role or override to update:", opts)
		if err != nil {
			if err.Error() == "interrupt" {
				return nil
			}

			term.OutputErrorAndExit("Error selecting setting or role: %v", err)
			return nil
		}

		idx := 0
		for i, opt := range opts {
			if opt == selection {
				idx = i
				break
			}
		}

		if idx == 0 {
			var opts []string
			for _, ms := range builtInModelPacks {
				opts = append(opts, "Built-in | "+ms.Name)
			}

			term.StartSpinner("")
			customModelPacks, apiErr := api.Client.ListModelPacks()
			term.StopSpinner()

			if apiErr != nil {
				term.OutputErrorAndExit("Error getting custom model packs: %v", apiErr)
				return nil
			}

			for _, ms := range customModelPacks {
				opts = append(opts, "Custom | "+ms.Name)
			}

			opts = append(opts, lib.GoBack)

			selection, err := term.SelectFromList("Select a model pack:", opts)
			if err != nil {
				if err.Error() == "interrupt" {
					return nil
				}

				term.OutputErrorAndExit("Error selecting model pack: %v", err)
				return nil
			}

			if selection == lib.GoBack {
				return updateModelSettings([]string{}, originalSettings)
			}

			var idx int
			for i, opt := range opts {
				if opt == selection {
					idx = i
					break
				}
			}

			if idx < len(builtInModelPacks) {
				modelPack = builtInModelPacks[idx]
			} else {
				modelPack = customModelPacks[idx-len(builtInModelPacks)]
			}

		} else if idx < len(shared.AllModelRoles)+1 {
			role = shared.AllModelRoles[idx-1]
		} else {
			settingDasherized = shared.ModelOverridePropsDasherized[idx-(len(shared.AllModelRoles)+1)]
			settingCompact = shared.Compact(settingDasherized)
		}
	}

	if modelPack == nil {
		if len(args) > 1 {
			if role != "" {
				propertyCompact = strings.ToLower(shared.Compact(args[1]))
			} else {
				value = args[1]
			}
		}

		if len(args) > 2 {
			value = args[2]
		}

		if settingCompact != "" {
			if value == "" {
				var err error
				value, err = term.GetUserStringInput(fmt.Sprintf("Set %s (leave blank for no value)", settingDasherized))
				if err != nil {
					if err.Error() == "interrupt" {
						return nil
					}

					term.OutputErrorAndExit("Error getting value: %v", err)
					return nil
				}
			}

			switch settingCompact {
			case "maxconvotokens":
				if value == "" {
					settings.ModelOverrides.MaxConvoTokens = nil
				} else {
					n, err := strconv.Atoi(value)
					if err != nil {
						fmt.Println("Invalid value for max-convo-tokens:", value)
						return nil
					}
					settings.ModelOverrides.MaxConvoTokens = &n
				}
			case "maxtokens":
				if value == "" {
					settings.ModelOverrides.MaxTokens = nil
				} else {
					n, err := strconv.Atoi(value)
					if err != nil {
						fmt.Println("Invalid value for max-tokens:", value)
						return nil
					}
					settings.ModelOverrides.MaxTokens = &n
				}
			}
		}

		if role != "" {
			if !(propertyCompact == "temperature" || propertyCompact == "topp") {
				term.StartSpinner("")
				customModels, apiErr := api.Client.ListCustomModels()
				term.StopSpinner()

				if apiErr != nil {
					term.OutputErrorAndExit("Error fetching models: %v", apiErr)
				}

				builtInModels := shared.FilterBuiltInCompatibleModels(shared.BuiltInBaseModels, role)
				customModels = shared.FilterCustomCompatibleModels(customModels, role)

				customModelsById := map[shared.ModelId]*shared.CustomModel{}
				for _, m := range customModels {
					customModelsById[m.ModelId] = m
				}

				modelIds := []shared.ModelId{}
				for _, m := range builtInModels {
					modelIds = append(modelIds, m.ModelId)
				}
				for _, m := range customModels {
					modelIds = append(modelIds, m.ModelId)
				}

				for _, id := range modelIds {
					if propertyCompact == fmt.Sprintf("%s/%s", role, shared.Compact(string(id))) {
						selectedModelId = id
						break
					}
				}
			}

			if selectedModelId == "" && propertyCompact == "" {
			Outer:
				for {
					opts := []string{
						"Select a model",
						"Set temperature",
						"Set top-p",
						"Set reserved output tokens",
					}

					opts = append(opts, lib.GoBack)

					selection, err := term.SelectFromList("Select a property to update:", opts)
					if err != nil {
						if err.Error() == "interrupt" {
							return nil
						}

						term.OutputErrorAndExit("Error selecting property: %v", err)
						return nil
					}

					if selection == lib.GoBack {
						return updateModelSettings([]string{}, originalSettings)
					}

					if selection == "Select a model" {
						term.StartSpinner("")
						customModels, apiErr := api.Client.ListCustomModels()
						term.StopSpinner()

						if apiErr != nil {
							term.OutputErrorAndExit("Error fetching models: %v", apiErr)
						}

						selectedModelId = lib.SelectModelIdForRole(customModels, role)

						if selectedModelId != "" {
							break Outer
						}
					} else if selection == "Set temperature" {
						propertyCompact = "temperature"
						break Outer
					} else if selection == "Set top-p" {
						propertyCompact = "topp"
						break Outer
					} else if selection == "Set reserved output tokens" {
						propertyCompact = "reservedoutputtokens"
						break Outer
					}
				}
			}

			if selectedModelId == "" {
				if propertyCompact != "" {
					if value == "" {
						msg := "Set"
						if propertyCompact == "temperature" {
							msg += "temperature (-2.0 to 2.0)"
						} else if propertyCompact == "topp" {
							msg += "top-p (0.0 to 1.0)"
						} else if propertyCompact == "reservedoutputtokens" {
							msg += "reserved output tokens"
						}
						var err error
						value, err = term.GetRequiredUserStringInput(msg)
						if err != nil {
							if err.Error() == "interrupt" {
								return nil
							}

							term.OutputErrorAndExit("Error getting value: %v", err)
							return nil
						}
					}

					switch propertyCompact {
					case "temperature":
						f, err := strconv.ParseFloat(value, 32)
						if err != nil || f < -2.0 || f > 2.0 {
							fmt.Println("Invalid value for temperature:", value)
							return nil
						}
						temperature = &f
					case "topp":
						f, err := strconv.ParseFloat(value, 32)
						if err != nil || f < 0.0 || f > 1.0 {
							fmt.Println("Invalid value for top-p:", value)
							return nil
						}
						topP = &f
					case "reservedoutputtokens":
						n, err := strconv.Atoi(value)
						if err != nil {
							fmt.Println("Invalid value for reserved-output-tokens:", value)
							return nil
						}
						settings.ModelOverrides.ReservedOutputTokens = &n
					}
				}
			}

			if settings.ModelPack == nil {
				settings.ModelPack = shared.DefaultModelPack
			}

			switch role {
			case shared.ModelRolePlanner:
				if selectedModelId != "" {
					settings.ModelPack.Planner.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.Planner.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.Planner.TopP = float32(*topP)
				} else if reservedOutputTokens != nil {
					settings.ModelPack.Planner.ReservedOutputTokens = *reservedOutputTokens
				}

			case shared.ModelRoleArchitect:
				if selectedModelId != "" {
					settings.ModelPack.Architect.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.Architect.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.Architect.TopP = float32(*topP)
				}

			case shared.ModelRoleCoder:
				if selectedModelId != "" {
					settings.ModelPack.Coder.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.Coder.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.Coder.TopP = float32(*topP)
				}

			case shared.ModelRolePlanSummary:
				if selectedModelId != "" {
					settings.ModelPack.PlanSummary.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.PlanSummary.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.PlanSummary.TopP = float32(*topP)
				}

			case shared.ModelRoleBuilder:
				if selectedModelId != "" {
					settings.ModelPack.Builder.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.Builder.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.Builder.TopP = float32(*topP)
				}

			case shared.ModelRoleWholeFileBuilder:
				if selectedModelId != "" {
					settings.ModelPack.WholeFileBuilder.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.WholeFileBuilder.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.WholeFileBuilder.TopP = float32(*topP)
				}

			case shared.ModelRoleName:
				if selectedModelId != "" {
					settings.ModelPack.Namer.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.Namer.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.Namer.TopP = float32(*topP)
				}

			case shared.ModelRoleCommitMsg:
				if selectedModelId != "" {
					settings.ModelPack.CommitMsg.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.CommitMsg.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.CommitMsg.TopP = float32(*topP)
				}

			case shared.ModelRoleExecStatus:
				if selectedModelId != "" {
					settings.ModelPack.ExecStatus.ModelId = selectedModelId
				} else if temperature != nil {
					settings.ModelPack.ExecStatus.Temperature = float32(*temperature)
				} else if topP != nil {
					settings.ModelPack.ExecStatus.TopP = float32(*topP)
				}
			}
		}
	} else {
		settings.ModelPack = modelPack
	}

	if reflect.DeepEqual(originalSettings, settings) {
		fmt.Println("🤷‍♂️ No model settings were updated")
		return nil
	} else {
		return settings
	}
}
