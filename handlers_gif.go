package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rs/zerolog/log"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

type gifRequest struct {
	Phone         string
	Gif           string
	Caption       string
	Id            string
	Seconds       uint32
	ContextInfo   waE2E.ContextInfo
	MentionAll    bool
	QuotedMessage *waE2E.Message `json:"QuotedMessage,omitempty"`
}

func (s *server) SendGIF() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, fetchVideoMaxBytes+(1<<20))
		request := gifRequest{}
		seconds := uint32(gifDefaultSecs)
		var source *mediaFile
		var err error

		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
			if err = r.ParseMultipartForm(1 << 20); err != nil {
				if r.MultipartForm != nil {
					r.MultipartForm.RemoveAll()
				}
				s.Respond(w, r, http.StatusBadRequest, errors.New("invalid or oversized GIF upload (100 MiB maximum)"))
				return
			}
			if r.MultipartForm != nil {
				defer r.MultipartForm.RemoveAll()
			}
			request.Phone = r.FormValue("Phone")
			request.Caption = r.FormValue("Caption")
			request.Id = r.FormValue("Id")
			if value := r.FormValue("MentionAll"); value != "" {
				request.MentionAll, err = strconv.ParseBool(value)
				if err != nil {
					s.Respond(w, r, http.StatusBadRequest, errors.New("MentionAll must be true or false"))
					return
				}
			}
			if value := r.FormValue("ContextInfo"); value != "" {
				if err = json.Unmarshal([]byte(value), &request.ContextInfo); err != nil {
					s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode ContextInfo"))
					return
				}
			}
			if value := r.FormValue("QuotedMessage"); value != "" {
				if err = json.Unmarshal([]byte(value), &request.QuotedMessage); err != nil {
					s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode QuotedMessage"))
					return
				}
			}
			seconds, err = parseGIFSeconds(r.FormValue("Seconds"))
			if err != nil {
				s.Respond(w, r, http.StatusBadRequest, err)
				return
			}
			file, header, fileErr := r.FormFile("Gif")
			if fileErr != nil {
				s.Respond(w, r, http.StatusBadRequest, errors.New("Gif file is required"))
				return
			}
			source, err = stageGIFUpload(r.Context(), file, header.Header.Get("Content-Type"), fetchVideoMaxBytes)
			file.Close()
		} else {
			var supplied gifRequest
			if err = json.NewDecoder(r.Body).Decode(&supplied); err != nil {
				s.Respond(w, r, http.StatusBadRequest, errors.New("could not decode Payload"))
				return
			}
			request = supplied
			seconds = request.Seconds
			if seconds == 0 {
				seconds = gifDefaultSecs
			}
			if seconds > gifMaxSeconds {
				s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("Seconds must be between 1 and %d", gifMaxSeconds))
				return
			}
			if request.Gif == "" {
				s.Respond(w, r, http.StatusBadRequest, errors.New("missing Gif in Payload"))
				return
			}
			source, err = readOutgoingMedia(r.Context(), request.Gif, fetchVideoMaxBytes)
		}
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("could not read GIF input: %w", err))
			return
		}
		defer source.Close()

		if request.Phone == "" {
			s.Respond(w, r, http.StatusBadRequest, errors.New("missing Phone in Payload"))
			return
		}
		client := clientManager.GetWhatsmeowClient(r.Context().Value("userinfo").(Values).Get("Id"))
		if client == nil {
			s.Respond(w, r, http.StatusInternalServerError, errors.New("no session"))
			return
		}
		recipient, err := validateMessageFields(r.Context(), client, request.Phone, request.ContextInfo.StanzaID, request.ContextInfo.Participant)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}
		mimeType, err := source.sniff()
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, fmt.Errorf("could not identify GIF input: %w", err))
			return
		}
		if !isGIFVideoInput(mimeType) {
			s.Respond(w, r, http.StatusBadRequest, errors.New("Gif must contain an animated GIF or video"))
			return
		}

		converted, thumbnail, err := convertGIFPlayback(r.Context(), source, seconds)
		if err != nil {
			s.Respond(w, r, http.StatusUnprocessableEntity, err)
			return
		}
		defer converted.Close()
		uploaded, err := uploadGIFMedia(r.Context(), client, converted, recipient)
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("failed to upload GIF video: %w", err))
			return
		}

		messageID := request.Id
		if messageID == "" {
			messageID = client.GenerateMessageID()
		}
		contextInfo := request.ContextInfo
		if request.QuotedMessage != nil {
			contextInfo.QuotedMessage = request.QuotedMessage
		}
		if contextInfo.StanzaID != nil && contextInfo.QuotedMessage == nil {
			contextInfo.QuotedMessage = &waE2E.Message{Conversation: proto.String("")}
		}
		mentions, err := resolveAllMentions(r.Context(), recipient, contextInfo.MentionedJID, request.Caption, request.MentionAll, client.GetGroupInfo)
		if err != nil {
			s.Respond(w, r, http.StatusBadRequest, err)
			return
		}
		contextInfo.MentionedJID = mergeMentionJIDs(mentions, request.Caption)
		videoMessage := &waE2E.VideoMessage{
			URL:            proto.String(uploaded.URL),
			DirectPath:     proto.String(uploaded.DirectPath),
			MediaKey:       uploaded.MediaKey,
			Mimetype:       proto.String("video/mp4"),
			FileSHA256:     uploaded.FileSHA256,
			FileEncSHA256:  uploaded.FileEncSHA256,
			FileLength:     proto.Uint64(uint64(converted.Size)),
			Seconds:        proto.Uint32(seconds),
			Caption:        proto.String(request.Caption),
			GifPlayback:    proto.Bool(true),
			GifAttribution: waE2E.VideoMessage_NONE.Enum(),
			JPEGThumbnail:  thumbnail,
			ContextInfo:    &contextInfo,
		}
		if contextInfo.IsForwarded != nil && *contextInfo.IsForwarded {
			videoMessage.ContextInfo.IsForwarded = proto.Bool(true)
		}
		message := &waE2E.Message{VideoMessage: videoMessage}
		response, err := sendMessageWithRecovery(
			r.Context().Value("userinfo").(Values).Get("Id"), client, r.Context(), recipient, message,
			whatsmeow.SendRequestExtra{ID: messageID},
		)
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, fmt.Errorf("error sending GIF: %w", err))
			return
		}

		userinfo := r.Context().Value("userinfo").(Values)
		historyLimit, _ := strconv.Atoi(userinfo.Get("History"))
		s.saveOutgoingMessageToHistory(userinfo.Get("Id"), recipient.String(), messageID, "video", request.Caption, "", response.Timestamp, historyLimit, message)
		s.publishSentMessageEvent(userinfo.Get("Token"), userinfo.Get("Id"), userinfo.Get("Id"), recipient, messageID, message, response.Timestamp)
		log.Info().Str("id", messageID).Str("recipient", recipient.String()).Msg("GIF message sent")
		result, err := json.Marshal(map[string]interface{}{
			"Details": "GIF sent", "Timestamp": response.Timestamp.Unix(), "Id": messageID, "Seconds": seconds,
		})
		if err != nil {
			s.Respond(w, r, http.StatusInternalServerError, err)
			return
		}
		s.Respond(w, r, http.StatusOK, string(result))
	}
}
