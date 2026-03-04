//go:build !embed_ui

package main

import "net/http"

func uiHandler() http.Handler {
	return nil
}
