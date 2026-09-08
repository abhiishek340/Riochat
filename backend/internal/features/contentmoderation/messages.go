package contentmoderation

import "strings"

// MessageStatusBlocked is the status of a withdrawn message.
const MessageStatusBlocked = "blocked"

// MessageModeration is the optional "moderation" object of a message response.
type MessageModeration struct {
	State      string   `json:"state,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	EventID    string   `json:"eventID,omitempty"`
	Categories []string `json:"categories,omitempty"`
}

// BuildMessageModeration derives the moderation view of a message from its
// run and its own fields. It returns nil when there is neither an event id nor
// a state worth showing (a run that never needed moderation has none).
//
// isUser tells whether the message is the user's; a blocked user message
// reports direction input, any other blocked message output.
func BuildMessageModeration(runState, messageStatus, eventID string, isUser bool, messageCategories, runCategories []string) *MessageModeration {
	state := strings.TrimSpace(runState)
	if state == StateNotRequired {
		state = ""
	}
	blocked := messageStatus == MessageStatusBlocked
	if blocked {
		state = StateBlocked
	}
	if state == "" && eventID == "" {
		return nil
	}
	m := &MessageModeration{State: state, EventID: eventID}
	if blocked {
		if isUser {
			m.Direction = DirectionInput
		} else {
			m.Direction = DirectionOutput
		}
	}
	m.Categories = messageCategories
	if len(m.Categories) == 0 {
		m.Categories = runCategories
	}
	if len(m.Categories) == 0 {
		m.Categories = nil
	}
	return m
}

// BlockedPlaceholder is what administrators see instead of a blocked user
// message in log and export views.
func BlockedPlaceholder(eventID string) string {
	if strings.TrimSpace(eventID) == "" {
		return "[blocked by content moderation]"
	}
	return "[blocked by content moderation; event " + eventID + "]"
}

// ExcludeFromModelContext reports whether a message must be left out of the
// context sent to models in later turns.
func ExcludeFromModelContext(messageStatus string) bool {
	return messageStatus == MessageStatusBlocked
}

// RedactedUserContent is the content an administrator sees for a blocked user
// message in log and export views (its attachments are shown as []).
func RedactedUserContent(eventID string) string { return BlockedPlaceholder(eventID) }

// RedactedErrorMessage keeps an existing error message of a blocked assistant
// message and otherwise substitutes the placeholder. Assistant content itself
// is shown empty, with attachments [] and no trace.
func RedactedErrorMessage(existing, eventID string) string {
	if strings.TrimSpace(existing) != "" {
		return existing
	}
	return BlockedPlaceholder(eventID)
}
