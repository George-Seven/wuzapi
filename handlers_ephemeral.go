package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow/types"
)

type chatDisappearingTimerRequest struct {
	Phone    string `json:"Phone"`
	Duration string `json:"Duration"`
}

func (s *server) SetChatDisappearingTimer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var request chatDisappearingTimerRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode payload"))
			return
		}
		if request.Phone == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing Phone in payload"))
			return
		}

		var duration time.Duration
		switch request.Duration {
		case "24h":
			duration = 24 * time.Hour
		case "7d":
			duration = 7 * 24 * time.Hour
		case "90d":
			duration = 90 * 24 * time.Hour
		case "off":
			duration = 0
		default:
			s.Respond(w, r, http.StatusBadRequest, errors.New("invalid Duration; use 24h, 7d, 90d, or off"))
			return
		}

		userID := r.Context().Value("userinfo").(Values).Get("Id")
		client := clientManager.GetWhatsmeowClient(userID)
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}
		chat, err := validateMessageFields(r.Context(), client, request.Phone, nil, nil)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}
		if chat.Server != types.DefaultUserServer && chat.Server != types.HiddenUserServer {
			s.Respond(w, r, http.StatusBadRequest, errors.New("Phone must identify a direct chat user, not a group or channel"))
			return
		}
		if err = client.SetDisappearingTimer(r.Context(), chat, duration, time.Now()); err != nil {
			log.Error().Err(err).Str("chat", chat.String()).Msg("failed to set direct chat disappearing timer")
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to set disappearing timer: %w", err))
			return
		}
		response, err := json.Marshal(map[string]interface{}{
			"Details":  "Disappearing timer set successfully",
			"ChatJID":  chat.String(),
			"Duration": request.Duration,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}
		s.Respond(w, r, http.StatusOK, string(response))
	}
}
