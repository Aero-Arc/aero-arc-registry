package redis

import (
	"embed"
	"fmt"
	"strings"

	redisclient "github.com/redis/go-redis/v9"
)

// luaFiles keeps the Redis transaction programs in syntax-highlighted source
// files while embedding them into the Registry binary at build time.
//
//go:embed scripts/*.lua
var luaFiles embed.FS

func mustLua(name string) string {
	source, err := luaFiles.ReadFile("scripts/" + name)
	if err != nil {
		panic(fmt.Sprintf("embed Redis Lua script %q: %v", name, err))
	}
	return string(source)
}

func newRedisScript(name string, preludes ...string) *redisclient.Script {
	return redisclient.NewScript(strings.Join(preludes, "") + mustLua(name))
}

var (
	redisTimeLua        = mustLua("redis_time.lua")
	entityValidationLua = mustLua("entity_validation.lua")
	agentValidationLua  = entityValidationLua + mustLua("agent_validation.lua")

	registerRelayScript = newRedisScript(
		"register_relay.lua", redisTimeLua, entityValidationLua,
	)
	heartbeatRelayScript = newRedisScript(
		"heartbeat_relay.lua", redisTimeLua, entityValidationLua,
	)
	readLiveRelayScript = newRedisScript(
		"read_live_relay.lua", redisTimeLua, entityValidationLua,
	)
	validateRelayIncarnationScript = newRedisScript(
		"validate_relay_incarnation.lua", redisTimeLua, entityValidationLua,
	)
	removeRelayScript = newRedisScript("remove_relay.lua")

	registerAgentScript = newRedisScript(
		"register_agent.lua", redisTimeLua, entityValidationLua,
	)
	heartbeatAgentScript = newRedisScript(
		"heartbeat_agent.lua", redisTimeLua, agentValidationLua,
	)
	liveAgentScript      = newRedisScript("live_agent.lua", agentValidationLua)
	readLiveAgentScript  = newRedisScript("read_live_agent.lua", agentValidationLua)
	readRelayAgentScript = newRedisScript("read_relay_agent.lua", agentValidationLua)
	removeAgentsScript   = newRedisScript("remove_agents.lua", entityValidationLua)
	activeIndexScript    = newRedisScript("active_index.lua", redisTimeLua)
)
