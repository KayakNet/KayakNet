# KayakNet Android App

Native Android client for the KayakNet anonymous P2P network.

## Features

- Connect to KayakNet P2P network
- Anonymous chat rooms
- P2P marketplace with escrow (Monero/Zcash)
- .kyk domain registration and lookup
- End-to-end encryption
- Onion routing (3-hop)
- Traffic analysis resistance
- Background service for persistent connection
- Push notifications for messages

## Requirements

- Android 8.0 (API 26) or higher
- Internet connection

## Building

### Prerequisites

- Android Studio Hedgehog (2023.1.1) or newer
- JDK 17
- Android SDK 34

### Build APK

```bash
cd apps/android
./gradlew assembleRelease
```

The APK will be at `app/build/outputs/apk/release/app-release.apk`

### Build Debug

```bash
./gradlew assembleDebug
```

## Installation

1. Enable "Install from unknown sources" in Android settings
2. Download the APK
3. Install and open KayakNet
4. The app will automatically connect to the network

## Architecture

```
net.kayaknet.app/
  KayakNetApp.kt          - Application class
  MainActivity.kt         - Main activity with Compose
  network/
    KayakNetClient.kt     - P2P network client
    KayakNetService.kt    - Background service
    BootReceiver.kt       - Auto-start on boot
  ui/
    KayakNetApp.kt        - Main navigation
    theme/
      Theme.kt            - Terminal green theme
      Type.kt             - Monospace typography
    screens/
      HomeScreen.kt       - Network status
      ChatScreen.kt       - Anonymous chat
      MarketScreen.kt     - P2P marketplace
      DomainsScreen.kt    - .kyk domains
      SettingsScreen.kt   - App settings
```

## Security

- Ed25519 node identity
- E2E encryption for all messages
- Onion routing with 3 hops
- Traffic padding and mixing
- No logs, no tracking

## Network Protocol

The Android client connects to the KayakNet bootstrap node and participates in:
- DHT for peer discovery
- Gossip protocol for message propagation
- Onion routing for anonymity

## License

MIT License - See LICENSE file


