package epoch

type Epoch int

const (
	EventUnknown Epoch = iota
	EventSunset
	EventSunrise
)

var stateName = map[Epoch]string{
	EventUnknown: "unknown",
	EventSunset:  "sunset",
	EventSunrise: "sunrise",
}

func (e Epoch) String() string {
	if name, ok := stateName[e]; ok {
		return name
	}
	return "unknown"
}
