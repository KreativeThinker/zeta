package com.zeta.android.ui.theme

import androidx.compose.material3.Typography
import androidx.compose.ui.text.TextStyle
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp

// Uses system sans-serif (Roboto on Android) until downloadable font certs are configured
val ZetaTypography = Typography(
    displayLarge   = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Bold,     fontSize = 28.sp, letterSpacing = (-0.5).sp),
    titleLarge     = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Bold,     fontSize = 22.sp, letterSpacing = (-0.3).sp),
    titleMedium    = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 16.sp),
    titleSmall     = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.SemiBold, fontSize = 14.sp),
    bodyLarge      = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Normal,   fontSize = 16.sp),
    bodyMedium     = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Normal,   fontSize = 14.sp),
    bodySmall      = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Normal,   fontSize = 12.sp),
    labelLarge     = TextStyle(fontFamily = FontFamily.SansSerif, fontWeight = FontWeight.Medium,   fontSize = 14.sp),
    labelSmall     = TextStyle(fontFamily = FontFamily.Monospace, fontWeight = FontWeight.Normal,   fontSize = 11.sp, letterSpacing = 0.5.sp),
)

// Expose for direct use in screens (e.g. mono IP addresses)
val JetBrainsMonoFamily: FontFamily = FontFamily.Monospace
