package entities

const TGMessageParseModeMarkdown = "Markdown"

type TGInlineKeyboardButtonStyle int

const (
	TGInlineKeyboardButtonStyleDefault TGInlineKeyboardButtonStyle = iota
	TGInlineKeyboardButtonStylePrimary
	TGInlineKeyboardButtonStyleSuccess
	TGInlineKeyboardButtonStyleDanger
)

func (s TGInlineKeyboardButtonStyle) String() string {
	switch s {
	case TGInlineKeyboardButtonStylePrimary:
		return "primary"
	case TGInlineKeyboardButtonStyleSuccess:
		return "success"
	case TGInlineKeyboardButtonStyleDanger:
		return "danger"
	}
	return ""
}