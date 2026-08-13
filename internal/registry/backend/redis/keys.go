package redis

import "encoding/base64"

func (b *Backend) relaysKey() string { return b.namespace + "relays" }
func (b *Backend) agentsKey() string { return b.namespace + "agents" }
func (b *Backend) relayKey(id string) string {
	return b.relayKeyPrefix() + encodeID(id)
}
func (b *Backend) agentKey(id string) string {
	return b.namespace + "agent:" + encodeID(id)
}
func (b *Backend) relayAgentsKey(id string) string {
	return b.relayAgentsKeyPrefix() + encodeID(id)
}
func (b *Backend) relayKeyPrefix() string       { return b.namespace + "relay:" }
func (b *Backend) relayAgentsKeyPrefix() string { return b.namespace + "relay-agents:" }
func (b *Backend) relayIncarnationKey() string  { return b.namespace + "relay-incarnation-sequence" }

func encodeID(id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id))
}
