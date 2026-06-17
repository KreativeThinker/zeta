package com.zeta.android.ui.theme

import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

private val ZetaColorScheme = lightColorScheme(
    primary          = ZetaAccent,
    onPrimary        = Color.White,
    primaryContainer = ZetaBg,
    onPrimaryContainer = ZetaAccent,
    background       = ZetaBg,
    onBackground     = ZetaText,
    surface          = ZetaSurface,
    onSurface        = ZetaAccent,
    surfaceVariant   = ZetaBg,
    onSurfaceVariant = ZetaText,
    outline          = ZetaBorder,
    error            = ZetaDanger,
    onError          = Color.White,
)

@Composable
fun ZetaTheme(content: @Composable () -> Unit) {
    MaterialTheme(
        colorScheme = ZetaColorScheme,
        typography = ZetaTypography,
        content = content,
    )
}
