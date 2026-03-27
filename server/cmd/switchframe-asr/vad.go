package main

import "math"

type vadState int

const (
	vadIdle     vadState = iota
	vadSpeaking
	vadTrailing
)

type energyVAD struct {
	thresholdLinear float64
	hangoverSamples int
	sampleRate      int
	currentState    vadState
	silentSamples   int
}

func newEnergyVAD(thresholdDB float64, hangoverMs, sampleRate int) *energyVAD {
	return &energyVAD{
		thresholdLinear: math.Pow(10, thresholdDB/20.0),
		hangoverSamples: sampleRate * hangoverMs / 1000,
		sampleRate:      sampleRate,
	}
}

func (v *energyVAD) state() vadState { return v.currentState }

func (v *energyVAD) process(samples []float32) {
	if len(samples) == 0 {
		return
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	isSpeech := rms >= v.thresholdLinear

	switch v.currentState {
	case vadIdle:
		if isSpeech {
			v.currentState = vadSpeaking
			v.silentSamples = 0
		}
	case vadSpeaking:
		if isSpeech {
			v.silentSamples = 0
		} else {
			v.currentState = vadTrailing
			v.silentSamples = len(samples)
		}
	case vadTrailing:
		if isSpeech {
			v.currentState = vadSpeaking
			v.silentSamples = 0
		} else {
			v.silentSamples += len(samples)
			if v.silentSamples >= v.hangoverSamples {
				v.currentState = vadIdle
				v.silentSamples = 0
			}
		}
	}
}

func (v *energyVAD) isSpeaking() bool {
	return v.currentState == vadSpeaking || v.currentState == vadTrailing
}

func (v *energyVAD) setThresholdDB(db float64) {
	v.thresholdLinear = math.Pow(10, db/20.0)
}

func (v *energyVAD) thresholdDB() float64 {
	return 20.0 * math.Log10(v.thresholdLinear)
}

func (v *energyVAD) reset() {
	v.currentState = vadIdle
	v.silentSamples = 0
}
