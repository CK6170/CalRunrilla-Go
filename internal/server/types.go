package server

import "time"

type APIError struct {
	Error string `json:"error"`
}

type HealthResponse struct {
	OK        bool      `json:"ok"`
	Timestamp time.Time `json:"timestamp"`
}

type UploadResponse struct {
	ConfigID string `json:"configId"`
	Kind     string `json:"kind"` // "config" or "calibrated"
}

type ConnectRequest struct {
	ConfigID string `json:"configId"`
}

type ConnectResponse struct {
	Connected bool   `json:"connected"`
	Port      string `json:"port"`
	Bars      int    `json:"bars"`
	LCs       int    `json:"lcs"`
}

type CalPlanResponse struct {
	Steps []CalStepDTO `json:"steps"`
}

type CalStepDTO struct {
	StepIndex int    `json:"stepIndex"`
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Prompt    string `json:"prompt"`
}

type CalStartStepRequest struct {
	StepIndex int `json:"stepIndex"`
}

type FlashStartRequest struct {
	CalibratedID string `json:"calibratedId"`
}

