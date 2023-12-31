package cli

const (
	noColor = -1
)

// UiColor is a posix shell color code to use.
type UiColor struct {
	Code int
	Bold bool
}

// A list of colors that are useful. These are all non-bolded by default.
var (
	UiColorNone    UiColor = UiColor{noColor, false}
	UiColorRed             = UiColor{int(ColorFgHiRed), false}
	UiColorGreen           = UiColor{int(ColorFgHiGreen), false}
	UiColorYellow          = UiColor{int(ColorFgHiYellow), false}
	UiColorBlue            = UiColor{int(ColorFgHiBlue), false}
	UiColorMagenta         = UiColor{int(ColorFgHiMagenta), false}
	UiColorCyan            = UiColor{int(ColorFgHiCyan), false}
)

// ColoredUi is a Ui implementation that colors its output according
// to the given color schemes for the given type of output.
type ColoredUi struct {
	OutputColor UiColor
	InfoColor   UiColor
	ErrorColor  UiColor
	WarnColor   UiColor
	Ui          Ui
}

func (u *ColoredUi) Ask(query string) (string, error) {
	return u.Ui.Ask(u.colorize(query, u.OutputColor))
}

func (u *ColoredUi) AskSecret(query string) (string, error) {
	return u.Ui.AskSecret(u.colorize(query, u.OutputColor))
}

func (u *ColoredUi) Output(message string) {
	u.Ui.Output(u.colorize(message, u.OutputColor))
}

func (u *ColoredUi) Info(message string) {
	u.Ui.Info(u.colorize(message, u.InfoColor))
}

func (u *ColoredUi) Error(message string) {
	u.Ui.Error(u.colorize(message, u.ErrorColor))
}

func (u *ColoredUi) Warn(message string) {
	u.Ui.Warn(u.colorize(message, u.WarnColor))
}

func (u *ColoredUi) colorize(message string, uc UiColor) string {
	if uc.Code == noColor {
		return message
	}

	attr := []ColorAttribute{ColorAttribute(uc.Code)}
	if uc.Bold {
		attr = append(attr, ColorBold)
	}

	return NewColor(attr...).SprintFunc()(message)
}
