package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// RunLocalDeploy executes the local deploy flow using the backend's local deploy payload
func RunLocalDeploy(cmd *cobra.Command, projectID, buildVersion string) error {
	client, err := getClient()
	if err != nil {
		return err
	}

	fmt.Printf("Fetching local deploy instructions for project %s, build %s...\n", projectID, buildVersion)

	// 1. Fetch the payload
	payload, err := client.DeployLocal(projectID, buildVersion)
	if err != nil {
		return fmt.Errorf("local deploy failed: %w", err)
	}

	executionId, _ := payload["executionId"].(string)

	// 2. Validate Secrets
	requiredSecretsRaw, ok := payload["requiredSecrets"].([]interface{})
	if ok && len(requiredSecretsRaw) > 0 {
		var required []string
		for _, v := range requiredSecretsRaw {
			required = append(required, v.(string))
		}
		if err := validateLocalSecrets(required); err != nil {
			fmt.Printf("\nMissing local secrets! You can import them interactively:\n")
			fmt.Printf("  pyxcloud secrets import --provider aws (or azure/gcp etc.)\n\n")
			fmt.Printf("Or set them manually:\n")
			for _, sec := range required {
				fmt.Printf("  pyxcloud secrets set %s=value\n", sec)
			}
			return fmt.Errorf("missing required secrets: %v", err)
		}
	}

	// 3. Create temp workspace
	workspaceDir, err := os.MkdirTemp("", "pyxcloud-deploy-*")
	if err != nil {
		return fmt.Errorf("failed to create temp workspace: %w", err)
	}
	defer os.RemoveAll(workspaceDir) // Clean up on exit

	fmt.Printf("Created temporary workspace at: %s\n", workspaceDir)

	// 4. Write Files
	filesData, ok := payload["files"].(map[string]interface{})
	if ok {
		for fPath, pFilesRaw := range filesData {
			pFiles := pFilesRaw.(map[string]interface{})
			fullDir := filepath.Join(workspaceDir, fPath)
			if err := os.MkdirAll(fullDir, 0755); err != nil {
				return err
			}
			for fileName, contentRaw := range pFiles {
				fullFile := filepath.Join(fullDir, fileName)
				if err := os.WriteFile(fullFile, []byte(contentRaw.(string)), 0644); err != nil {
					return err
				}
			}
		}
	}

	// 5. Load standard PyxCloud env values to a file (to simulate GITHUB_ENV)
	pyxEnvFile := filepath.Join(workspaceDir, "pyxenv.txt")
	os.WriteFile(pyxEnvFile, []byte(""), 0644) // Emtpy touch

	// Setup environment
	localSecrets, _ := loadSecrets()
	envVariables := os.Environ()
	for k, v := range localSecrets {
		envVariables = append(envVariables, fmt.Sprintf("%s=%s", k, v))
	}
	envVariables = append(envVariables, "PYXCLOUD_ENV_FILE="+pyxEnvFile)

	// 6. Execute Steps
	stepsRaw, ok := payload["steps"].([]interface{})
	if !ok {
		return fmt.Errorf("no steps returned from backend")
	}

	fmt.Println("\nStarting Terraform Execution...")
	for idx, sRaw := range stepsRaw {
		step := sRaw.(map[string]interface{})
		name := step["name"].(string)
		runScript := step["run"].(string)

		// Create a shell script for this step so we can reliably execute complex bash
		stepScriptPath := filepath.Join(workspaceDir, fmt.Sprintf("step_%d.sh", idx))
		scriptContent := fmt.Sprintf("#!/bin/bash\nset -e\n%s", runScript)
		os.WriteFile(stepScriptPath, []byte(scriptContent), 0755)

		fmt.Printf("\n▸ \033[1;36m%s\033[0m\n", name)

		execCmd := exec.Command("bash", stepScriptPath)
		execCmd.Dir = workspaceDir

		// Setup custom step environment
		stepEnv := append([]string{}, envVariables...)
		if envMapRaw, ok := step["env"].(map[string]interface{}); ok {
			for k, v := range envMapRaw {
				stepEnv = append(stepEnv, fmt.Sprintf("%s=%s", k, v.(string)))
			}
		}
		execCmd.Env = stepEnv

		// Stream Output
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			fmt.Printf("\n\033[1;31mStep Failed: %s\033[0m\n", err.Error())
			fmt.Print("Do you want to abort the deployment? (Y/n): ")
			reader := bufio.NewReader(os.Stdin)
			text, _ := reader.ReadString('\n')
			if strings.TrimSpace(strings.ToLower(text)) != "n" {
				return fmt.Errorf("deployment aborted by user")
			}
		}

		// Read back PYXCLOUD_ENV_FILE to update current environment variables for next steps
		if envFileData, err := os.ReadFile(pyxEnvFile); err == nil {
			lines := strings.Split(string(envFileData), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				idx := strings.Index(line, "=")
				if idx > 0 {
					envVariables = append(envVariables, line)
				}
			}
			os.WriteFile(pyxEnvFile, []byte(""), 0644) // Clear it for next step
		}
	}

	fmt.Println("\n\033[1;32mLocal Execution Completed Successfully!\033[0m")

	// 7. Extract tfstates to commit back
	fmt.Println("Collecting terraform states to commit back to PyxCloud...")
	tfstates := make(map[string]string)
	if filesData != nil {
		for fPath := range filesData {
			fullDir := filepath.Join(workspaceDir, fPath)
			statePath := filepath.Join(fullDir, "terraform.tfstate")
			if stateData, err := os.ReadFile(statePath); err == nil {
				tfstates[fPath] = string(stateData)
			}
		}
	}

	if len(tfstates) == 0 {
		fmt.Println("No terraform.tfstate files found to commit.")
		return nil
	}

	// 8. Commit back to backend (requires StepUp)
	fmt.Println("\nTo commit your infrastructure state to PyxCloud, step-up authentication is required.")
	// Generate JWT context internally (bypass performStepUpWebflow logic inside client methods, just prompt UI)
	fmt.Println("Completing deployment...")
	
	stepUpToken, err := ProvideStepUpToken(client, "stepup")
	if err != nil {
		return fmt.Errorf("failed to retrieve step-up token: %w", err)
	}

	result, err := client.DeployComplete(projectID, buildVersion, executionId, stepUpToken, tfstates)
	if err != nil {
		return fmt.Errorf("failed to commit tfstate: %w", err)
	}

	fmt.Printf("\n%v\n", result["message"])
	return nil
}

func validateLocalSecrets(required []string) error {
	secrets, err := loadSecrets()
	if err != nil {
		return err
	}
	missing := []string{}
	for _, req := range required {
		if val, exists := secrets[req]; !exists || val == "" {
			missing = append(missing, req)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s", strings.Join(missing, ", "))
	}
	return nil
}

// ProvideStepUpToken abstracts the browser webflow for step up.
func ProvideStepUpToken(client *clientWrapper, audience string) (string, error) {
	fmt.Println("Opening browser for Step-Up Authentication (MFA)...")
	// The client has PerformStepUpWebflow, we can use it.
	// We need to type assert or use the standard client
	// wait, 'client' in RunLocalDeploy is '*Client' standard package, but in cmd we have `getClient` which returns an interface
	// let's do this directly.
	return PerformStepUpWebflow(audience)
}

func PerformStepUpWebflow(audience string) (string, error) {
	client, err := getClient()
	if err != nil {
		return "", err
	}
	// The real PerformStepUpWebflow is defined in credentials_oauth.go or we can call it.
	// But it requires an access token to poll.
	// Wait, the Keystore recover calls `token, err := performStepUpWebflow("stepup")`
	// Let's rely on that existing function in the cmd package!
	return performStepUpWebflow(audience)
}

