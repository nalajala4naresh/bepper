// Minimal stand-in for BuildBuddy's app/capabilities/capabilities.ts. The
// ported app/terminal component only reads config.cspNonce (for the
// AutoSizer's inline <style> nonce), so that's all this provides.
export default {
  config: {
    cspNonce: "",
  },
};
