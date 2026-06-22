package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

const slackWebhookTimeout = 15 * time.Second

func (s *Server) notifyTools() []toolEntry {
	tool := mcp.NewTool("notify_post_slack",
		mcp.WithDescription(
			"Post a compact Slack message via incoming webhook (mutating). "+
				"Set SLACK_WEBHOOK_URL on the server or pass webhook_url. "+
				"Chain after digest summaries or CI triage notes — do not paste raw logs."),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(true),
		mcp.WithString("text", mcp.Required(), mcp.Description("Plain-text Slack message (keep under ~2KB)")),
		mcp.WithString("webhook_url", mcp.Description("Override SLACK_WEBHOOK_URL env for this call")),
	)

	return []toolEntry{
		{tool: tool, handler: s.handleNotifyPostSlack},
	}
}

func (s *Server) handleNotifyPostSlack(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := request.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"pass non-empty text")), nil
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return mcp.NewToolResultError(formatToolError(ErrValidation, "text is empty",
			"pass a short summary, not raw logs")), nil
	}

	url := strings.TrimSpace(request.GetString("webhook_url", ""))
	if url == "" {
		url = strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL"))
	}
	if url == "" {
		return mcp.NewToolResultError(formatToolError(ErrValidation,
			"no Slack webhook URL configured",
			"set SLACK_WEBHOOK_URL on the MCP server or pass webhook_url")), nil
	}

	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrGeneric, err.Error(),
			"retry with shorter text")), nil
	}

	reqCtx, cancel := context.WithTimeout(ctx, slackWebhookTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return mcp.NewToolResultError(formatToolError(ErrValidation, err.Error(),
			"check webhook_url format")), nil
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		code := ErrGeneric
		hint := "verify webhook URL and network reachability"
		if reqCtx.Err() != nil {
			code = ErrTransient
			hint = "Slack webhook timed out — retry once"
		}
		return mcp.NewToolResultError(formatToolError(code, err.Error(), hint)), nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		preview := text
		if len(preview) > 80 {
			preview = preview[:77] + "..."
		}
		return mcp.NewToolResultText(formatToolOK(fmt.Sprintf("Slack message posted (%d chars): %q",
			len(text), preview))), nil
	}

	code := ErrGeneric
	hint := "verify webhook URL is valid and channel exists"
	if resp.StatusCode == 429 {
		code = ErrRateLimit
		hint = "Slack rate-limited — wait and retry"
	} else if resp.StatusCode >= 500 {
		code = ErrTransient
		hint = "Slack server error — retry once"
	}
	return mcp.NewToolResultError(formatToolError(code,
		fmt.Sprintf("Slack webhook returned HTTP %d", resp.StatusCode), hint)), nil
}
