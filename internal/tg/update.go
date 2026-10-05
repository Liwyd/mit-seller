package tg

// Update is the subset of Telegram's Update the bot reads.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type PhotoSize struct {
	FileID string `json:"file_id"`
}

type Video struct {
	FileID string `json:"file_id"`
}

type Contact struct {
	PhoneNumber string `json:"phone_number"`
	UserID      int64  `json:"user_id"`
}

type Entity struct {
	Type          string `json:"type"`
	Offset        int    `json:"offset"`
	Length        int    `json:"length"`
	CustomEmojiID string `json:"custom_emoji_id"`
}

type Message struct {
	MessageID       int64       `json:"message_id"`
	From            *User       `json:"from"`
	Chat            Chat        `json:"chat"`
	Text            string      `json:"text"`
	Caption         string      `json:"caption"`
	Photo           []PhotoSize `json:"photo"`
	Video           *Video      `json:"video"`
	Contact         *Contact    `json:"contact"`
	ReplyToMessage  *Message    `json:"reply_to_message"`
	ForwardFrom     *User       `json:"forward_from"`
	Entities        []Entity    `json:"entities"`
	CaptionEntities []Entity    `json:"caption_entities"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message"`
	Data    string   `json:"data"`
}

// Fields flattens an update into the variables botapi.php derived from it.
type Fields struct {
	FromID          int64
	ChatType        string
	Text            string
	TextCallback    string
	MessageID       int64
	HasPhoto        bool
	PhotoID         string
	Caption         string
	HasVideo        bool
	VideoID         string
	Datain          string
	Username        string
	FirstName       string
	UserPhone       string
	ContactID       int64
	CallbackQueryID string
	Entities        []Entity // text entities (for custom emoji)
	CaptionEntities []Entity
}

// Flatten mirrors botapi.php's variable extraction, including its fallbacks.
func (u *Update) Flatten() Fields {
	var f Fields
	m, cq := u.Message, u.CallbackQuery
	if m != nil && m.From != nil {
		f.FromID = m.From.ID
	} else if cq != nil {
		f.FromID = cq.From.ID
	}
	if m != nil {
		f.ChatType = m.Chat.Type
	} else if cq != nil && cq.Message != nil {
		f.ChatType = cq.Message.Chat.Type
	}
	if m != nil {
		f.Text = m.Text
		f.MessageID = m.MessageID
		f.Entities = m.Entities
		f.CaptionEntities = m.CaptionEntities
		if len(m.Photo) > 0 {
			f.HasPhoto = true
			f.PhotoID = m.Photo[len(m.Photo)-1].FileID
		}
		f.Caption = m.Caption
		if m.Video != nil {
			f.HasVideo = true
			f.VideoID = m.Video.FileID
		}
		if m.Contact != nil {
			f.UserPhone = m.Contact.PhoneNumber
			f.ContactID = m.Contact.UserID
		}
	}
	if cq != nil {
		if cq.Message != nil {
			f.TextCallback = cq.Message.Text
			if m == nil {
				f.MessageID = cq.Message.MessageID
				f.Caption = cq.Message.Caption
			}
		}
		f.Datain = cq.Data
		f.CallbackQueryID = cq.ID
	}
	f.Username = "NOT_USERNAME"
	if m != nil && m.From != nil && m.From.Username != "" {
		f.Username = m.From.Username
	} else if cq != nil && cq.From.Username != "" {
		f.Username = cq.From.Username
	}
	if m != nil && m.From != nil {
		f.FirstName = m.From.FirstName
	} else if cq != nil {
		f.FirstName = cq.From.FirstName
	}
	return f
}
