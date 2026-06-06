package a2a

// buildCard assembles the agent card from the wrapped agent's identity plus
// server options. Called once at NewServer time; served verbatim after.
func buildCard(name, description string, o *serverOptions) AgentCard {
	if o.cardOverride != nil {
		return *o.cardOverride // WithCard replaces everything
	}
	card := AgentCard{
		Name:        name,
		Description: description,
		Version:     o.version,
		Capabilities: AgentCapabilities{
			Streaming:         true,
			PushNotifications: o.pushEnabled,
		},
		Skills:             o.skills,
		SecuritySchemes:    o.securitySchemes,
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}
	if o.url != "" {
		// One mount serves both transports, so advertise both interfaces.
		card.SupportedInterfaces = []AgentInterface{
			{URL: o.url, ProtocolBinding: BindingJSONRPC, ProtocolVersion: "1.0"},
			{URL: o.url, ProtocolBinding: BindingHTTPJSON, ProtocolVersion: "1.0"},
		}
	}
	return card
}
