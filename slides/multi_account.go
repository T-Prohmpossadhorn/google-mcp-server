package slides

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"go.ngs.io/google-mcp-server/auth"
	"go.ngs.io/google-mcp-server/server"
	"google.golang.org/api/drive/v3"
	"google.golang.org/api/option"
)

type MultiAccountService struct {
	authManager *auth.AccountManager
}

func NewMultiAccountService(authManager *auth.AccountManager) *MultiAccountService {
	return &MultiAccountService{
		authManager: authManager,
	}
}

func (s *MultiAccountService) GetTools() []server.Tool {
	return []server.Tool{
		{
			Name:        "slides_presentations_list_all_accounts",
			Description: "List presentations from all authenticated Google accounts",
			InputSchema: server.InputSchema{
				Type: "object",
				Properties: map[string]server.Property{
					"max_results": {
						Type:        "number",
						Description: "Maximum number of presentations per account",
					},
				},
			},
		},
	}
}

func (s *MultiAccountService) HandleToolCall(ctx context.Context, name string, arguments json.RawMessage) (interface{}, error) {
	switch name {
	case "slides_presentations_list_all_accounts":
		var args map[string]interface{}
		if err := json.Unmarshal(arguments, &args); err != nil {
			return nil, err
		}

		maxResults := 10
		if mr, ok := args["max_results"].(float64); ok {
			maxResults = int(mr)
		}

		accounts := s.authManager.ListAccounts()
		accountResults := map[string]interface{}{}
		totalCount := 0

		for _, account := range accounts {
			// Skip if no OAuth client
			if account.OAuthClient == nil {
				continue
			}

			// The Slides API has no list endpoint; presentations are Drive
			// files, so list them through the Drive API.
			driveService, err := drive.NewService(ctx, option.WithHTTPClient(account.OAuthClient.GetHTTPClient()))
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create drive service for %s: %v\n", account.Email, err)
				accountResults[account.Email] = map[string]interface{}{"error": err.Error()}
				continue
			}

			list, err := driveService.Files.List().
				Q("mimeType='application/vnd.google-apps.presentation' and trashed = false").
				OrderBy("modifiedTime desc").
				PageSize(int64(maxResults)).
				Fields("files(id, name, modifiedTime, webViewLink)").
				Do()
			if err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to list presentations for %s: %v\n", account.Email, err)
				accountResults[account.Email] = map[string]interface{}{"error": err.Error()}
				continue
			}

			presentations := make([]map[string]interface{}, 0, len(list.Files))
			for _, file := range list.Files {
				presentations = append(presentations, map[string]interface{}{
					"presentation_id": file.Id,
					"title":           file.Name,
					"modified_time":   file.ModifiedTime,
					"url":             file.WebViewLink,
				})
			}

			accountResults[account.Email] = map[string]interface{}{
				"count":         len(presentations),
				"presentations": presentations,
			}
			totalCount += len(presentations)
		}

		return map[string]interface{}{
			"account_count": len(accounts),
			"accounts":      accountResults,
			"total_count":   totalCount,
		}, nil

	default:
		return nil, fmt.Errorf("unknown tool: %s", name)
	}
}
