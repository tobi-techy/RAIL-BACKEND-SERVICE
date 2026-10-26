package entities

// TextSource says where the text of an inbound turn came from. The platform
// stamps it, never the sender: it travels to the Python agent as
// message_source so text that nobody typed (read off a photo, written by the
// platform) cannot be mistaken for an instruction.
type TextSource string

const (
	// TextSourceUser is text the sender typed.
	TextSourceUser TextSource = "user"
	// TextSourceVoice is the sender's own words, transcribed from a voice note.
	TextSourceVoice TextSource = "voice"
	// TextSourceVision is text read off a photo by OCR and summarised by code.
	TextSourceVision TextSource = "vision"
	// TextSourceSystem is text the platform wrote itself, such as a statement
	// scan summary standing in for what the sender sent.
	TextSourceSystem TextSource = "system"
)
