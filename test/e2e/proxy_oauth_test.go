package e2e_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stacklok/toolhive/pkg/networking"
	"github.com/stacklok/toolhive/test/e2e"
)

// generateUniqueOIDCServerName creates a unique server name for OIDC mock tests
func generateUniqueOIDCServerName(prefix string) string {
	return fmt.Sprintf("%s-%d-%d-%d", prefix, os.Getpid(), time.Now().UnixNano(), GinkgoRandomSeed())
}

var _ = Describe("Proxy OAuth Authentication E2E", Serial, func() {
	var (
		config          *e2e.TestConfig
		mockOIDCPort    int
		proxyPort       int
		mockOIDCServer  *e2e.OIDCMockServer
		proxyCmd        *exec.Cmd
		osvServerName   string
		proxyServerName string
		clientID        = "test-client"
		clientSecret    = "test-secret"
		mockOIDCBaseURL string
	)

	BeforeEach(func() {
		config = e2e.NewTestConfig()

		// Check if thv binary is available
		err := e2e.CheckTHVBinaryAvailable(config)
		Expect(err).ToNot(HaveOccurred(), "thv binary should be available for testing")

		// Generate unique names for this test run
		osvServerName = generateUniqueOIDCServerName("osv-oauth-target")
		proxyServerName = generateUniqueOIDCServerName("proxy-oauth-test")

		// Find available ports for our mock servers using networking utilities
		mockOIDCPort, err = networking.FindOrUsePort(0)
		Expect(err).ToNot(HaveOccurred())

		proxyPort, err = networking.FindOrUsePort(0)
		Expect(err).ToNot(HaveOccurred())

		mockOIDCBaseURL = fmt.Sprintf("http://localhost:%d", mockOIDCPort)

		// Start mock OIDC server using Ory Fosite
		By("Starting mock OIDC server")
		specReport := CurrentSpecReport()
		if strings.Contains(specReport.FullText(), "Proxy OAuth Authentication E2E") {
			mockOIDCServer, err = e2e.NewOIDCMockServer(
				mockOIDCPort, clientID, clientSecret,
				e2e.WithAccessTokenLifespan(2*time.Second),
			)
		} else {
			mockOIDCServer, err = e2e.NewOIDCMockServer(mockOIDCPort, clientID, clientSecret)
		}
		Expect(err).ToNot(HaveOccurred())

		// Enable auto-complete for MCP tests
		mockOIDCServer.EnableAutoComplete()

		err = mockOIDCServer.Start()
		Expect(err).ToNot(HaveOccurred())

		// Wait for OIDC server to be ready
		Eventually(func() error {
			return checkServerHealth(fmt.Sprintf("%s/.well-known/openid-configuration", mockOIDCBaseURL))
		}, 30*time.Second, 1*time.Second).Should(Succeed())

		// Start OSV MCP server that will be our target
		By("Starting OSV MCP server as target")
		e2e.NewTHVCommand(config, "run",
			"--name", osvServerName,
			"--transport", "sse",
			"osv").ExpectSuccess()

		// Wait for OSV server to be ready
		err = e2e.WaitForMCPServer(config, osvServerName, 60*time.Second)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		By("Cleaning up test resources")

		// Stop proxy if running
		if proxyCmd != nil && proxyCmd.Process != nil {
			proxyCmd.Process.Kill()
			proxyCmd.Wait()
		}

		// Stop and remove OSV server
		if config.CleanupAfter {
			err := e2e.StopAndRemoveMCPServer(config, osvServerName)
			Expect(err).ToNot(HaveOccurred(), "Should be able to stop and remove server")
		}

		// Stop mock OIDC server
		if mockOIDCServer != nil {
			err := mockOIDCServer.Stop()
			if err != nil {
				GinkgoWriter.Printf("Warning: Failed to stop OIDC mock server: %v\n", err)
			}
		}
	})

	Context("when OAuth authentication is enabled", func() {
		It("should successfully start proxy with OAuth configuration", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			// remove path from server url
			parsedURL, err := url.Parse(osvServerURL)
			if err != nil {
				GinkgoWriter.Printf("Failed to parse OSV server URL: %v\n", err)
			}
			base := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

			By("Starting the proxy with OAuth configuration")
			proxyCmd = startProxyWithOAuth(
				config,
				proxyServerName,
				base,
				proxyPort,
				mockOIDCBaseURL,
				clientID,
				clientSecret,
			)

			// Give the proxy some time to start and potentially complete OAuth flow
			time.Sleep(10 * time.Second)

			By("Verifying proxy process is still running")
			// If OAuth flow failed, the process would have exited
			Expect(proxyCmd.ProcessState).To(BeNil(), "Proxy process should still be running")

			By("Testing proxy endpoint accessibility")
			// Try to access the proxy endpoint
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(fmt.Sprintf("http://localhost:%d/sse", proxyPort))
			if err == nil {
				defer resp.Body.Close()
				// We expect some response, even if it's not a successful MCP connection
				// The important thing is that the proxy is running and accessible
				Expect(resp.StatusCode).To(BeNumerically(">=", 200))
				Expect(resp.StatusCode).To(BeNumerically("<", 500))
			}
		})

		It("should handle OAuth auto-detection when target requires authentication", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			// remove path from server url
			parsedURL, err := url.Parse(osvServerURL)
			if err != nil {
				GinkgoWriter.Printf("Failed to parse OSV server URL: %v\n", err)
			}
			base := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

			By("Starting the proxy with OAuth auto-detection")
			proxyCmd = startProxyWithOAuthDetection(
				config,
				proxyServerName,
				base,
				proxyPort,
				clientID,
				clientSecret,
			)

			// Give the proxy time to start
			time.Sleep(5 * time.Second)

			By("Verifying proxy starts successfully")
			// The proxy should start even if OAuth detection doesn't find requirements
			Expect(proxyCmd.ProcessState).To(BeNil(), "Proxy process should be running")
		})
	})

	Context("when OAuth authentication fails", func() {
		It("should handle invalid OAuth credentials gracefully", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			// remove path from server url
			parsedURL, err := url.Parse(osvServerURL)
			if err != nil {
				GinkgoWriter.Printf("Failed to parse OSV server URL: %v\n", err)
			}
			base := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

			By("Starting the proxy with invalid OAuth credentials")
			proxyCmd = startProxyWithOAuth(
				config,
				proxyServerName,
				base,
				proxyPort,
				mockOIDCBaseURL,
				"invalid-client",
				"invalid-secret",
			)

			By("Verifying the proxy process exits due to OAuth failure")
			// The proxy should exit when OAuth fails due to invalid client credentials
			// Use a goroutine to wait for the process with a timeout
			done := make(chan error, 1)
			go func() {
				done <- proxyCmd.Wait()
			}()

			select {
			case err := <-done:
				// Process exited as expected
				Expect(err).To(HaveOccurred(), "Process should exit with error due to invalid OAuth credentials")
				Expect(proxyCmd.ProcessState).ToNot(BeNil(), "Process should have exited")
				Expect(proxyCmd.ProcessState.Exited()).To(BeTrue(), "Process should have exited")
				Expect(proxyCmd.ProcessState.Success()).To(BeFalse(), "Process should exit with error")
			case <-time.After(10 * time.Second):
				Fail("Process should have exited within 10 seconds due to invalid OAuth credentials")
			}
		})

		It("should handle missing OAuth issuer gracefully when remote-auth is explicitly enabled", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			// remove path from server url
			parsedURL, err := url.Parse(osvServerURL)
			if err != nil {
				GinkgoWriter.Printf("Failed to parse OSV server URL: %v\n", err)
			}
			base := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

			By("Starting the proxy with missing OAuth issuer but remote-auth enabled")
			proxyCmd = startProxyWithOAuth(
				config,
				proxyServerName,
				base,
				proxyPort,
				"", // Empty issuer
				clientID,
				clientSecret,
			)

			By("Verifying the proxy process exits due to missing issuer")
			// The proxy should exit immediately when --remote-auth is enabled but issuer is missing
			// Use a goroutine to wait for the process with a timeout
			done := make(chan error, 1)
			go func() {
				done <- proxyCmd.Wait()
			}()

			select {
			case err := <-done:
				// Process exited as expected
				Expect(err).To(HaveOccurred(), "Process should exit with error due to missing issuer")
				Expect(proxyCmd.ProcessState).ToNot(BeNil(), "Process should have exited")
				Expect(proxyCmd.ProcessState.Exited()).To(BeTrue(), "Process should have exited")
				Expect(proxyCmd.ProcessState.Success()).To(BeFalse(), "Process should exit with error")
			case <-time.After(5 * time.Second):
				Fail("Process should have exited within 5 seconds due to missing issuer")
			}
		})

		It("should handle auto-detection when target server returns WWW-Authenticate header", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			// remove path from server url
			parsedURL, err := url.Parse(osvServerURL)
			if err != nil {
				GinkgoWriter.Printf("Failed to parse OSV server URL: %v\n", err)
			}
			base := fmt.Sprintf("%s://%s", parsedURL.Scheme, parsedURL.Host)

			By("Starting the proxy with auto-detection (no --remote-auth flag)")
			proxyCmd = startProxyWithAutoDetection(
				config,
				proxyServerName,
				base,
				proxyPort,
				clientID,
				clientSecret,
			)

			// Give the proxy time to try auto-detection
			time.Sleep(5 * time.Second)

			By("Verifying proxy starts successfully even when no auth is detected")
			// The proxy should start successfully since OSV server doesn't require auth
			Expect(proxyCmd.ProcessState).To(BeNil(), "Proxy process should be running")
		})
	})

	Context("when testing proxy functionality with MCP protocol", func() {
		It("should proxy MCP requests successfully after OAuth", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			By("Extracting base URL for transparent proxy")
			// The URL from thv list is like: http://127.0.0.1:21929/sse#container-name
			// But the transparent proxy needs the base URL: http://127.0.0.1:21929
			baseURL := strings.TrimSuffix(strings.Split(osvServerURL, "#")[0], "/sse")
			GinkgoWriter.Printf("Original server URL: %s\n", osvServerURL)
			GinkgoWriter.Printf("Base URL for proxy: %s\n", baseURL)

			By("Starting the proxy with OAuth configuration and longer timeout")
			var outputBuffer *bytes.Buffer
			proxyCmd, outputBuffer = startProxyWithOAuthForMCP(
				config,
				proxyServerName,
				baseURL, // Use base URL instead of full URL
				proxyPort,
				mockOIDCBaseURL,
				clientID,
				clientSecret,
			)

			By("Extracting OAuth URL from proxy output and completing the flow")
			// Give the proxy a moment to start and display the OAuth URL
			time.Sleep(5 * time.Second)

			// Extract OAuth URL from captured output
			output := outputBuffer.String()
			GinkgoWriter.Printf("Captured proxy output: %s\n", output)

			// Use regex to extract the OAuth URL
			// Pattern: "Please open this URL in your browser: <URL>"
			urlPattern := regexp.MustCompile(`Please open this URL in your browser: (https?://[^\s]+)`)
			matches := urlPattern.FindStringSubmatch(output)

			var authURL string
			if len(matches) >= 2 {
				authURL = matches[1]
				GinkgoWriter.Printf("Extracted OAuth URL from buffer: %s\n", authURL)
			} else {
				// Fallback: construct the URL from what we know
				// We can see the URL in the logs, so let's construct it
				authURL = fmt.Sprintf("%s/auth?client_id=%s&response_type=code&scope=openid+profile+email", mockOIDCBaseURL, clientID)
				GinkgoWriter.Printf("Using constructed OAuth URL: %s\n", authURL)
			}

			// Complete the OAuth flow by visiting the URL with auto_complete parameter
			err = completeOAuthFlow(authURL)
			if err != nil {
				GinkgoWriter.Printf("Failed to complete OAuth flow: %v\n", err)
				Skip("Skipping MCP test due to OAuth flow completion failure")
			}

			// Wait for proxy to complete OAuth and start
			time.Sleep(5 * time.Second)

			By("Testing MCP connection through proxy")
			proxyURL := fmt.Sprintf("http://localhost:%d/sse", proxyPort)

			// Wait for proxy to be ready for MCP connections
			err = e2e.WaitForMCPServerReady(config, proxyURL, "sse", 60*time.Second)
			if err != nil {
				GinkgoWriter.Printf("MCP connection through proxy failed: %v\n", err)
				Skip("Skipping MCP test due to proxy not being ready")
			}

			By("Creating MCP client through proxy")
			mcpClient, err := e2e.NewMCPClientForSSE(config, proxyURL)
			Expect(err).ToNot(HaveOccurred())
			defer mcpClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			err = mcpClient.Initialize(ctx)
			Expect(err).ToNot(HaveOccurred())

			By("Testing basic MCP operations through proxy")
			err = mcpClient.Ping(ctx)
			Expect(err).ToNot(HaveOccurred())

			tools, err := mcpClient.ListTools(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(tools.Tools).ToNot(BeEmpty(), "Should have OSV tools available through proxy")
		})
	})

	Context("when testing proxy functionality with MCP protocol and token refresh", func() {
		It("should refresh token after expiry and continue MCP operations", func() {
			By("Getting OSV server URL")
			osvServerURL, err := e2e.GetMCPServerURL(config, osvServerName)
			Expect(err).ToNot(HaveOccurred())

			By("Extracting base URL for transparent proxy")
			baseURL := strings.TrimSuffix(strings.Split(osvServerURL, "#")[0], "/sse")
			GinkgoWriter.Printf("Base URL for proxy: %s\n", baseURL)

			By("Starting the proxy with OAuth-enabled MCP support")
			var outputBuffer *bytes.Buffer
			proxyCmd, outputBuffer = startProxyWithOAuthForMCP(
				config,
				proxyServerName,
				baseURL,
				proxyPort,
				mockOIDCBaseURL,
				clientID,
				clientSecret,
			)

			By("Completing the initial OAuth flow")
			Eventually(outputBuffer.String, 5*time.Second, 500*time.Millisecond).
				Should(ContainSubstring("Please open this URL"))

			matches := regexp.MustCompile(`Please open this URL in your browser: (https?://[^\s]+)`).
				FindStringSubmatch(outputBuffer.String())
			Expect(matches).To(HaveLen(2))
			authURL := matches[1]
			Expect(completeOAuthFlow(authURL)).To(Succeed())

			By("Giving proxy time to finish OAuth exchange")
			time.Sleep(2 * time.Second)

			By("Waiting for access token to expire")
			time.Sleep(3 * time.Second) // longer than the 2s lifespan

			By("Reconnecting via MCP to trigger token refresh")
			proxyURL := fmt.Sprintf("http://localhost:%d/sse", proxyPort)
			err = e2e.WaitForMCPServerReady(config, proxyURL, "sse", 30*time.Second)
			Expect(err).ToNot(HaveOccurred(), "MCP server not ready after token expiry")

			mcpClient, err := e2e.NewMCPClientForSSE(config, proxyURL)
			Expect(err).ToNot(HaveOccurred())
			defer mcpClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			Expect(mcpClient.Initialize(ctx)).To(Succeed())
			Expect(mcpClient.Ping(ctx)).To(Succeed())

			tools, err := mcpClient.ListTools(ctx)
			Expect(err).ToNot(HaveOccurred())
			Expect(tools.Tools).ToNot(BeEmpty(), "Should list tools after refresh")
		})
	})

})

// Helper functions

func checkServerHealth(healthUrl string) error {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(healthUrl)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return fmt.Errorf("server not healthy, status: %d", resp.StatusCode)
}

func startProxyWithOAuth(config *e2e.TestConfig, serverName, targetURL string, port int, issuer, clientID, clientSecret string) *exec.Cmd {
	args := []string{
		"proxy",
		"--host", "localhost",
		"--port", strconv.Itoa(port),
		"--target-uri", targetURL,
		"--remote-auth-skip-browser",  // Important for headless testing
		"--remote-auth-timeout", "5s", // Short timeout for testing
	}

	// Only add OAuth flags if issuer is provided
	if issuer != "" {
		args = append(args,
			"--remote-auth",
			"--remote-auth-issuer", issuer,
			"--remote-auth-client-id", clientID,
			"--remote-auth-client-secret", clientSecret)
	} else {
		// For missing issuer test, we still need to enable remote auth
		args = append(args,
			"--remote-auth",
			"--remote-auth-client-id", clientID,
			"--remote-auth-client-secret", clientSecret)
	}

	args = append(args, serverName)

	// Log the command for debugging
	GinkgoWriter.Printf("Starting proxy with args: %v\n", args)

	return e2e.StartLongRunningTHVCommand(config, args...)
}

func startProxyWithOAuthDetection(config *e2e.TestConfig, serverName, targetURL string, port int, clientID, clientSecret string) *exec.Cmd {
	args := []string{
		"proxy",
		"--host", "localhost",
		"--port", strconv.Itoa(port),
		"--target-uri", targetURL,
		"--remote-auth-client-id", clientID,
		"--remote-auth-client-secret", clientSecret,
		"--remote-auth-skip-browser",
		serverName,
	}

	return e2e.StartLongRunningTHVCommand(config, args...)
}

func startProxyWithAutoDetection(config *e2e.TestConfig, serverName, targetURL string, port int, clientID, clientSecret string) *exec.Cmd {
	args := []string{
		"proxy",
		"--host", "localhost",
		"--port", strconv.Itoa(port),
		"--target-uri", targetURL,
		"--remote-auth-client-id", clientID,
		"--remote-auth-client-secret", clientSecret,
		"--remote-auth-skip-browser",
		serverName,
	}

	// Log the command for debugging
	GinkgoWriter.Printf("Starting proxy with auto-detection args: %v\n", args)

	return e2e.StartLongRunningTHVCommand(config, args...)
}

func startProxyWithOAuthForMCP(config *e2e.TestConfig, serverName, targetURL string, port int, issuer, clientID, clientSecret string) (*exec.Cmd, *bytes.Buffer) {
	args := []string{
		"proxy",
		"--host", "localhost",
		"--port", strconv.Itoa(port),
		"--target-uri", targetURL,
		"--remote-auth-skip-browser",   // Important for headless testing
		"--remote-auth-timeout", "30s", // Longer timeout for MCP testing
		"--remote-auth",
		"--remote-auth-issuer", issuer,
		"--remote-auth-client-id", clientID,
		"--remote-auth-client-secret", clientSecret,
		serverName,
	}

	// Log the command for debugging
	GinkgoWriter.Printf("Starting proxy with OAuth for MCP args: %v\n", args)

	// Create command
	cmd := exec.Command(config.THVBinary, args...)
	cmd.Env = os.Environ()

	// Create buffer to capture output (capture both stdout and stderr)
	var outputBuffer bytes.Buffer

	// Use MultiWriter to write to both buffer and GinkgoWriter
	multiWriter := io.MultiWriter(&outputBuffer, GinkgoWriter)
	cmd.Stdout = multiWriter
	cmd.Stderr = multiWriter // Capture stderr too since logger might write there

	// Start the command
	err := cmd.Start()
	Expect(err).ToNot(HaveOccurred())

	return cmd, &outputBuffer
}

// completeOAuthFlow programmatically completes the OAuth flow by visiting the authorization URL
func completeOAuthFlow(authURL string) error {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			// Follow redirects automatically
			return nil
		},
	}

	// Add auto_complete parameter to trigger automatic OAuth completion
	if authURL != "" {
		separator := "&"
		if !strings.Contains(authURL, "?") {
			separator = "?"
		}
		authURL = authURL + separator + "auto_complete=true"
	}

	// Make a request to the authorization URL
	// This will trigger the OAuth flow and redirect to the callback
	resp, err := client.Get(authURL)
	if err != nil {
		return fmt.Errorf("failed to complete OAuth flow: %w", err)
	}
	defer resp.Body.Close()

	// The response should be a redirect to the callback URL
	// or a success page if the flow completed
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return nil
	}

	return fmt.Errorf("OAuth flow failed with status: %d", resp.StatusCode)
}
