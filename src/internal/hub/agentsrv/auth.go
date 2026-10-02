package agentsrv

import (
	"context"
	"errors"

	"github.com/LanceLRQ/PiMon/src/internal/agentlink"
	"github.com/LanceLRQ/PiMon/src/internal/hub/devices"
	"github.com/LanceLRQ/PiMon/src/pkg/protocol/agent"
)

const (
	// credentialLen 是 agent.NewCredential 生成的凭据长度（32 字节的无填充 base64url）。
	credentialLen = 43
	// maxPairCodeLen 是可接受的配对码输入长度上限（32 位 Crockford 加分隔符与空白留足余量）。
	maxPairCodeLen = 64
)

// Authenticate 实现 agentlink.Authenticator：校验拨入设备的长期凭据，返回设备 ID。
// 凭据格式不对、不存在、哈希不符或属于拨出设备一律拒绝为 bad_credential；存储错误与 ctx 取消原样返回。
func (s *Server) Authenticate(ctx context.Context, msg agent.Auth) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(msg.Credential) != credentialLen {
		return "", &agentlink.AuthError{Reason: agent.ReasonBadCredential}
	}
	id, err := s.devs.VerifyCredential(ctx, msg.Credential)
	if errors.Is(err, devices.ErrNotFound) {
		return "", &agentlink.AuthError{Reason: agent.ReasonBadCredential}
	}
	if err != nil {
		return "", err
	}
	return id, nil
}

// Pair 实现 agentlink.Authenticator：消耗配对码、签发凭据并插入未确认的拨入设备占位
// （首个 hello 被接受时确认，重新配对的迁移也在那时执行）。
// 配对码为空、过长或不存在拒绝为 bad_code，已过期为 code_expired；存储错误与 ctx 取消原样返回。
func (s *Server) Pair(ctx context.Context, msg agent.Pair) (agent.PairOK, error) {
	if err := ctx.Err(); err != nil {
		return agent.PairOK{}, err
	}
	if msg.Code == "" || len(msg.Code) > maxPairCodeLen {
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonBadCode}
	}
	pc, err := s.devs.ConsumePairingCode(ctx, devices.HashPairingCode(msg.Code))
	switch {
	case errors.Is(err, devices.ErrCodeNotFound):
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonBadCode}
	case errors.Is(err, devices.ErrCodeExpired):
		return agent.PairOK{}, &agentlink.AuthError{Reason: agent.ReasonCodeExpired}
	case err != nil:
		return agent.PairOK{}, err
	}
	cred, err := agent.NewCredential()
	if err != nil {
		return agent.PairOK{}, err
	}
	hash := agent.CredentialHash(cred)
	if err := s.devs.CreatePaired(ctx, devices.PairedDevice{
		CredentialHash: hash, Code: pc, PairName: msg.Name, Hostname: msg.Hostname,
	}); err != nil {
		return agent.PairOK{}, err
	}
	id := agent.DeviceIDFromHash(hash)
	s.log.Info("agent 配对成功，已创建待确认的设备", "device_id", id, "repair_of", pc.RepairDeviceID)
	return agent.PairOK{
		Credential:   cred,
		DeviceID:     id,
		ListenerInfo: agent.ListenerInfo{Hostname: s.hostname(), Version: s.hubVersion},
	}, nil
}

// 编译期确认 Server 满足 agentlink.Authenticator。
var _ agentlink.Authenticator = (*Server)(nil)
