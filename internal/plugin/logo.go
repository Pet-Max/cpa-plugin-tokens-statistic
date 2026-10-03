package plugin

import "encoding/base64"

// pluginLogo is the management-panel menu logo: a token ring holding an
// ascending bar chart, inlined as a data URL so the registration payload is
// self-contained and no external asset hosting is required.
var pluginLogo = "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(pluginIconSVG))

// The muted gray tones intentionally match the sidebar icon palette of the
// management center. Hosts render the logo through an img element, where
// currentColor resolves to black rather than the sidebar tone. These two tones
// are the closest a static asset gets to the muted icon colours of both light
// and dark panel themes.
const pluginIconSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" fill="#72787c">
  <style>
    @media (prefers-color-scheme: dark) { :root { fill: #9c9d9b; } }
  </style>
  <path fill-rule="evenodd" d="M 256 20 A 236 236 0 1 0 256 492 A 236 236 0 1 0 256 20 Z M 256 72 A 184 184 0 1 1 256 440 A 184 184 0 1 1 256 72 Z"/>
  <rect x="128" y="246" width="64" height="130" rx="30"/>
  <rect x="224" y="191" width="64" height="185" rx="30"/>
  <rect x="320" y="136" width="64" height="240" rx="30"/>
</svg>
`
