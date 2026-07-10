package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"go.ngs.io/google-mcp-server/auth"
	"go.ngs.io/google-mcp-server/server"
)

// MultiAccountClient manages Docs operations across multiple accounts.
type MultiAccountClient struct {
	accountManager *auth.AccountManager
	clients        map[string]*Client
	mu             sync.RWMutex
}

// NewMultiAccountClient creates a new multi-account Docs client and eagerly
// builds a per-account client for every authenticated account.
func NewMultiAccountClient(ctx context.Context, accountManager *auth.AccountManager) (*MultiAccountClient, error) {
	mac := &MultiAccountClient{
		accountManager: accountManager,
		clients:        make(map[string]*Client),
	}

	for email, oauthClient := range accountManager.GetAllOAuthClients() {
		client, err := NewClient(ctx, oauthClient)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create docs service for %s: %v\n", email, err)
			continue
		}
		mac.clients[email] = client
	}

	return mac, nil
}

// GetClientForContext returns the Docs client for the account resolved from the
// provided hint (an email address), creating one on demand if needed.
func (mac *MultiAccountClient) GetClientForContext(ctx context.Context, hint string) (*Client, string, error) {
	account, err := mac.accountManager.GetAccountForContext(ctx, hint)
	if err != nil {
		return nil, "", err
	}

	mac.mu.RLock()
	client, exists := mac.clients[account.Email]
	mac.mu.RUnlock()
	if exists {
		return client, account.Email, nil
	}

	// Create the client on demand if it does not exist yet.
	newClient, err := NewClient(ctx, account.OAuthClient)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create docs service: %w", err)
	}

	mac.mu.Lock()
	mac.clients[account.Email] = newClient
	mac.mu.Unlock()

	return newClient, account.Email, nil
}

// MultiAccountHandler handles Docs operations with multi-account support.
type MultiAccountHandler struct {
	multiClient *MultiAccountClient
	handler     *Handler // used for tool definitions and resource calls
}

// NewMultiAccountHandler creates a new Docs handler with multi-account support.
// defaultClient may be nil; it is only used as a fallback for tool definitions.
func NewMultiAccountHandler(accountManager *auth.AccountManager, defaultClient *Client) *MultiAccountHandler {
	ctx := context.Background()
	multiClient, err := NewMultiAccountClient(ctx, accountManager)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to initialize docs multi-account client: %v\n", err)
		multiClient = &MultiAccountClient{
			accountManager: accountManager,
			clients:        make(map[string]*Client),
		}
	}

	return &MultiAccountHandler{
		multiClient: multiClient,
		handler:     NewHandler(defaultClient),
	}
}

// GetTools returns the Docs tools, each augmented with an optional account
// parameter so callers can target a specific account in multi-account setups.
func (h *MultiAccountHandler) GetTools() []server.Tool {
	tools := h.handler.GetTools()
	for i := range tools {
		if tools[i].InputSchema.Properties == nil {
			tools[i].InputSchema.Properties = make(map[string]server.Property)
		}
		tools[i].InputSchema.Properties["account"] = server.Property{
			Type:        "string",
			Description: "Email address of the account to use (optional; required when multiple accounts are authenticated)",
		}
	}
	return tools
}

// HandleToolCall routes a Docs tool call to the client for the requested account.
func (h *MultiAccountHandler) HandleToolCall(ctx context.Context, name string, arguments json.RawMessage) (interface{}, error) {
	var accountHint string
	if arguments != nil {
		var probe map[string]interface{}
		if err := json.Unmarshal(arguments, &probe); err == nil {
			if account, ok := probe["account"].(string); ok {
				accountHint = account
			}
		}
	}

	client, accountUsed, err := h.multiClient.GetClientForContext(ctx, accountHint)
	if err != nil {
		return nil, err
	}

	tempHandler := NewHandler(client)
	result, err := tempHandler.HandleToolCall(ctx, name, arguments)
	if err != nil {
		return nil, err
	}

	if resultMap, ok := result.(map[string]interface{}); ok {
		resultMap["account"] = accountUsed
	}
	return result, nil
}

// GetResources returns the available Docs resources.
func (h *MultiAccountHandler) GetResources() []server.Resource {
	return h.handler.GetResources()
}

// HandleResourceCall handles a resource call for the Docs service.
func (h *MultiAccountHandler) HandleResourceCall(ctx context.Context, uri string) (interface{}, error) {
	return h.handler.HandleResourceCall(ctx, uri)
}
