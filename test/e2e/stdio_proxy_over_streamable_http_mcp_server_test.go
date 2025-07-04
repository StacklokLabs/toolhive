package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/stacklok/toolhive/test/e2e"
)

var _ = Describe("TimeStreamableHttpMcpServer", Serial, func() {
	var config *e2e.TestConfig

	BeforeEach(func() {
		config = e2e.NewTestConfig()
		err := e2e.CheckTHVBinaryAvailable(config)
		Expect(err).ToNot(HaveOccurred(), "thv binary should be available")
	})

	Context("when starting the time server with streamable-http proxy", func() {
		var serverName string

		BeforeEach(func() {
			serverName = generateUniqueServerName("time-streamable-test")
		})

		AfterEach(func() {
			if config.CleanupAfter {
				err := e2e.StopAndRemoveMCPServer(config, serverName)
				Expect(err).ToNot(HaveOccurred(), "Should be able to stop and remove server")
			}
		})

		It("should respond to a single get_current_time request and a batch request", func() {
			By("Starting the time MCP server with streamable-http proxy")
			e2e.NewTHVCommand(config, "run",
				"--name", serverName,
				"--proxy-mode", "streamable-http",
				"time").ExpectSuccess()

			By("Waiting for the server to be running")
			err := e2e.WaitForMCPServer(config, serverName, 60*time.Second)
			Expect(err).ToNot(HaveOccurred())

			By("Getting the server URL")
			serverURL, err := e2e.GetMCPServerURL(config, serverName)
			Expect(err).ToNot(HaveOccurred())

			By("Waiting for MCP server to be ready")
			err = e2e.WaitForMCPServerReady(config, serverURL, "streamable-http", 60*time.Second)
			Expect(err).ToNot(HaveOccurred())

			By("Creating MCP client and initializing connection")
			mcpClient, err := e2e.NewMCPClientForStreamableHTTP(config, serverURL)
			Expect(err).ToNot(HaveOccurred())
			defer mcpClient.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err = mcpClient.Initialize(ctx)
			Expect(err).ToNot(HaveOccurred())

			By("Calling get_current_time tool")
			mcpClient.ExpectToolExists(ctx, "get_current_time")
			arguments := map[string]interface{}{
				"timezone": "Europe/London",
			}
			result := mcpClient.ExpectToolCall(ctx, "get_current_time", arguments)
			Expect(result.Content).ToNot(BeEmpty(), "Should return the current time")

			By("Sending a batch JSON-RPC request")
			batch := []map[string]interface{}{
				{
					"method": "tools/call",
					"params": map[string]interface{}{
						"name": "get_current_time",
						"arguments": map[string]interface{}{
							"timezone": "Asia/Karachi",
						},
					},
					"jsonrpc": "2.0",
					"id":      4,
				},
				{
					"method": "tools/call",
					"params": map[string]interface{}{
						"name": "convert_time",
						"arguments": map[string]interface{}{
							"source_timezone": "Asia/Karachi",
							"time":            "16:50",
							"target_timezone": "Europe/London",
						},
					},
					"jsonrpc": "2.0",
					"id":      5,
				},
			}

			batchBytes, err := json.Marshal(batch)
			Expect(err).ToNot(HaveOccurred())

			client := &http.Client{Timeout: 10 * time.Second}
			req, err := http.NewRequestWithContext(ctx, "POST", serverURL, bytes.NewReader(batchBytes))
			Expect(err).ToNot(HaveOccurred())
			req.Header.Set("Content-Type", "application/json")

			resp, err := client.Do(req)
			Expect(err).ToNot(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(200))

			var responses []map[string]interface{}
			decoder := json.NewDecoder(resp.Body)
			err = decoder.Decode(&responses)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(responses)).To(Equal(2))

			ids := map[float64]bool{4: false, 5: false}
			for _, r := range responses {
				id, ok := r["id"].(float64)
				Expect(ok).To(BeTrue(), "Each response should have an id")
				ids[id] = true
				Expect(r["result"]).ToNot(BeNil(), "Each response should have a result")
			}
			Expect(ids[4]).To(BeTrue())
			Expect(ids[5]).To(BeTrue())
		})
	})
})
