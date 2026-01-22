# KayakNet iOS App

A native iOS client for KayakNet - a privacy-first anonymous P2P network.

## Features

- **Home**: Network statistics and connection status
- **Chat**: Encrypted chat rooms with real-time messaging
- **Marketplace**: Browse listings, create orders with escrow protection
- **Domains**: Register and manage .kyk domains
- **Settings**: Profile and connection configuration

## Requirements

- macOS with Xcode 15.0+
- iOS 16.0+ deployment target
- Apple Developer Account (for device deployment)

## Building

### Option 1: Create Xcode Project

1. Open Xcode
2. Create a new iOS App project:
   - Product Name: `KayakNet`
   - Bundle Identifier: `net.kayaknet.app`
   - Interface: SwiftUI
   - Language: Swift

3. Replace the generated files with the files in `KayakNet/KayakNet/`:
   - `KayakNetApp.swift`
   - `ContentView.swift`
   - `Models/Models.swift`
   - `Network/KayakNetClient.swift`
   - `Views/*.swift`

4. Copy `Info.plist` to the project

5. Build and run (⌘+R)

### Option 2: Swift Package

```bash
cd KayakNet
swift build
```

## Project Structure

```
KayakNet/
├── KayakNetApp.swift          # App entry point
├── ContentView.swift          # Main tab view
├── Info.plist                 # App configuration
├── Assets.xcassets/           # App icons and colors
├── Models/
│   └── Models.swift           # Data models
├── Network/
│   └── KayakNetClient.swift   # API client
└── Views/
    ├── HomeView.swift         # Home screen
    ├── ChatView.swift         # Chat interface
    ├── MarketView.swift       # Marketplace
    ├── DomainsView.swift      # Domain management
    └── SettingsView.swift     # Settings
```

## Configuration

The app connects to the KayakNet bootstrap server at `203.161.33.237:8080`.

### App Transport Security

The app allows HTTP connections for the bootstrap server. This is configured in `Info.plist`:

```xml
<key>NSAppTransportSecurity</key>
<dict>
    <key>NSAllowsArbitraryLoads</key>
    <true/>
</dict>
```

## Marketplace Flow

1. Browse listings in the Market tab
2. Tap a listing to view details
3. Click "BUY NOW" to open order form
4. Select payment method (XMR/ZEC)
5. Enter delivery info
6. Confirm order
7. Copy payment address and send crypto
8. Track order status in "My Orders"

## Building for Distribution

### TestFlight (Recommended)

1. Archive the app in Xcode (Product → Archive)
2. Upload to App Store Connect
3. Distribute via TestFlight

### Ad-Hoc Distribution

1. Create provisioning profile with device UDIDs
2. Archive and export IPA
3. Distribute directly to devices

## License

KayakNet - Privacy First



