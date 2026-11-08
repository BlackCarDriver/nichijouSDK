package nichijouSDK

const (
	// SDKName identifies this SDK in the bridge handshake.
	SDKName = "nichijouSDK"
	// Version is the SDK release version. Update it for each SDK release;
	// games report the version compiled into their running server.
	Version = "0.1.0"
	// ProtocolVersion changes only for incompatible wire protocol changes.
	ProtocolVersion = "v1"
)
