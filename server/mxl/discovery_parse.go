package mxl

import "encoding/json"

// nmosFlowDef represents the subset of NMOS IS-04 flow definition we parse.
type nmosFlowDef struct {
	ID           string        `json:"id"`
	Format       string        `json:"format"`
	MediaType    string        `json:"media_type"`
	Label        string        `json:"label"`
	GrainRate    *nmosRational `json:"grain_rate"`
	SampleRate   *nmosRational `json:"sample_rate"`
	FrameWidth   int           `json:"frame_width"`
	FrameHeight  int           `json:"frame_height"`
	ChannelCount int           `json:"channel_count"`
}

type nmosRational struct {
	Numerator   int64 `json:"numerator"`
	Denominator int64 `json:"denominator"`
}

// parseFlowDef parses an NMOS IS-04 flow definition JSON into a FlowInfo.
func parseFlowDef(data []byte, flowID string) (FlowInfo, error) {
	var def nmosFlowDef
	if err := json.Unmarshal(data, &def); err != nil {
		return FlowInfo{}, err
	}

	info := FlowInfo{
		ID:        flowID,
		Name:      def.Label,
		MediaType: def.MediaType,
	}

	switch def.Format {
	case "urn:x-nmos:format:video":
		info.Format = DataFormatVideo
		info.Width = def.FrameWidth
		info.Height = def.FrameHeight
		if def.GrainRate != nil {
			info.GrainRate = Rational{
				Numerator:   def.GrainRate.Numerator,
				Denominator: def.GrainRate.Denominator,
			}
		}
	case "urn:x-nmos:format:audio":
		info.Format = DataFormatAudio
		info.Channels = def.ChannelCount
		if def.SampleRate != nil {
			info.SampleRate = int(def.SampleRate.Numerator)
			info.GrainRate = Rational{
				Numerator:   def.SampleRate.Numerator,
				Denominator: 1,
			}
			if def.SampleRate.Denominator > 0 {
				info.GrainRate.Denominator = def.SampleRate.Denominator
			}
		}
	case "urn:x-nmos:format:data":
		info.Format = DataFormatData
	default:
		info.Format = DataFormatUnspecified
	}

	return info, nil
}
