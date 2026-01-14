package net.kayaknet.app.ui.theme

import android.app.Activity
import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.runtime.SideEffect
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.toArgb
import androidx.compose.ui.platform.LocalView
import androidx.core.view.WindowCompat

// KayakNet terminal green theme
val KayakGreen = Color(0xFF00FF00)
val KayakGreenDim = Color(0xFF00AA00)
val KayakGreenDark = Color(0xFF004400)
val KayakBlack = Color(0xFF000000)
val KayakDarkGray = Color(0xFF0A0A0A)
val KayakAmber = Color(0xFFFFAA00)
val KayakRed = Color(0xFFFF4444)

private val DarkColorScheme = darkColorScheme(
    primary = KayakGreen,
    onPrimary = KayakBlack,
    primaryContainer = KayakGreenDark,
    onPrimaryContainer = KayakGreen,
    secondary = KayakGreenDim,
    onSecondary = KayakBlack,
    secondaryContainer = KayakGreenDark,
    onSecondaryContainer = KayakGreen,
    tertiary = KayakAmber,
    onTertiary = KayakBlack,
    background = KayakBlack,
    onBackground = KayakGreen,
    surface = KayakDarkGray,
    onSurface = KayakGreen,
    surfaceVariant = KayakDarkGray,
    onSurfaceVariant = KayakGreenDim,
    error = KayakRed,
    onError = KayakBlack,
    outline = KayakGreenDim
)

// Light theme (still dark for terminal feel)
private val LightColorScheme = DarkColorScheme

@Composable
fun KayakNetTheme(
    darkTheme: Boolean = true, // Always dark for terminal aesthetic
    content: @Composable () -> Unit
) {
    val colorScheme = DarkColorScheme
    val view = LocalView.current
    
    if (!view.isInEditMode) {
        SideEffect {
            val window = (view.context as Activity).window
            window.statusBarColor = KayakBlack.toArgb()
            WindowCompat.getInsetsController(window, view).isAppearanceLightStatusBars = false
        }
    }

    MaterialTheme(
        colorScheme = colorScheme,
        typography = Typography,
        content = content
    )
}

