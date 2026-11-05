package main

import (
	"errors"
	"net/url"
	"strings"
)

type AISettingsInput struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"`
	Key      string `json:"key"`
	ClearKey bool   `json:"clearKey"`
}

func (a *App) nextAIConfig(old AIConfig, body AISettingsInput) (AIConfig, error) {
	body.Endpoint = strings.TrimSpace(body.Endpoint)
	body.Model = strings.TrimSpace(body.Model)
	body.Key = strings.TrimSpace(body.Key)
	if len(body.Endpoint) > 2048 || len(body.Model) > 200 || len(body.Key) > 8192 {
		return AIConfig{}, errors.New("AI 配置内容过长")
	}
	if body.Enabled || body.Endpoint != "" {
		if _, err := aiEndpoint(body.Endpoint); err != nil {
			return AIConfig{}, err
		}
	}
	if body.Enabled && body.Model == "" {
		return AIConfig{}, errors.New("请填写 AI 模型名称")
	}
	next := AIConfig{Enabled: body.Enabled, Endpoint: body.Endpoint, Model: body.Model, Secret: old.Secret}
	if body.ClearKey {
		next.Secret = ""
	} else if strings.TrimSpace(body.Key) != "" {
		secret, err := a.encrypt(strings.TrimSpace(body.Key))
		if err != nil {
			return AIConfig{}, err
		}
		next.Secret = secret
	}
	if next.Endpoint != old.Endpoint && body.Key == "" && !body.ClearKey {
		oldURL, _ := url.Parse(old.Endpoint)
		newURL, _ := url.Parse(next.Endpoint)
		if oldURL == nil || newURL == nil || !strings.EqualFold(oldURL.Host, newURL.Host) {
			next.Secret = ""
		}
	}
	return next, nil
}
