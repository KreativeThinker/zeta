import com.google.protobuf.gradle.id

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
    id("org.jetbrains.kotlin.plugin.compose")
    id("org.jetbrains.kotlin.plugin.serialization")
    id("com.google.protobuf")
}

android {
    namespace = "com.zeta.android"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.zeta.android"
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "1.0.0"
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
            val keystoreFile = System.getenv("KEYSTORE_FILE")
            if (keystoreFile != null) {
                signingConfig =
                    signingConfigs.create("release").also {
                        it.storeFile = file(keystoreFile)
                        it.storePassword = System.getenv("KEYSTORE_PASSWORD")
                        it.keyAlias = System.getenv("KEY_ALIAS")
                        it.keyPassword = System.getenv("KEY_PASSWORD")
                    }
            }
        }
        debug {
            applicationIdSuffix = ".debug"
        }
    }

    buildFeatures {
        compose = true
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    sourceSets {
        for (variant in listOf("debug", "release")) {
            getByName(variant) {
                java.srcDirs(
                    "build/generated/source/proto/$variant/java",
                    "build/generated/source/proto/$variant/grpc",
                )
                kotlin.srcDirs(
                    "build/generated/source/proto/$variant/grpckt",
                    "build/generated/source/proto/$variant/kotlin",
                )
            }
        }
    }
}

kotlin {
    jvmToolchain(17)
}

protobuf {
    protoc {
        artifact = "com.google.protobuf:protoc:3.25.3"
    }
    plugins {
        id("grpc") {
            artifact = "io.grpc:protoc-gen-grpc-java:1.65.1"
        }
        id("grpckt") {
            artifact = "io.grpc:protoc-gen-grpc-kotlin:1.4.1:jdk8@jar"
        }
    }
    generateProtoTasks {
        all().forEach { task ->
            task.builtins {
                id("java") { option("lite") }
                id("kotlin") { option("lite") }
            }
            task.plugins {
                id("grpc") { option("lite") }
                id("grpckt")
            }
        }
    }
}

// Ensure proto generation runs before Kotlin compilation and generated sources are included
androidComponents {
    onVariants { variant ->
        val variantName = variant.name.replaceFirstChar { it.uppercase() }
        val generateTask = "generate${variantName}Proto"
        tasks.withType<org.jetbrains.kotlin.gradle.tasks.KotlinCompile>().configureEach {
            dependsOn(generateTask)
        }
    }
}

dependencies {
    val composeBom = platform("androidx.compose:compose-bom:2025.05.00")
    implementation(composeBom)
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.ui:ui-tooling-preview")
    implementation("androidx.compose.material3:material3")
    implementation("androidx.compose.material:material-icons-core")
    implementation("androidx.activity:activity-compose:1.9.2")
    implementation("androidx.navigation:navigation-compose:2.8.2")
    implementation("androidx.lifecycle:lifecycle-viewmodel-compose:2.8.6")
    implementation("androidx.lifecycle:lifecycle-runtime-compose:2.8.6")
    implementation("androidx.lifecycle:lifecycle-service:2.8.6")

    implementation("androidx.security:security-crypto:1.1.0-alpha06")

    implementation("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-android:1.8.1")
    implementation("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.8.1")

    implementation(files("libs/vpnlib.aar"))

    implementation("io.grpc:grpc-kotlin-stub:1.4.1")
    implementation("io.grpc:grpc-okhttp:1.65.1")
    implementation("io.grpc:grpc-android:1.65.1")
    implementation("io.grpc:grpc-stub:1.65.1")
    implementation("io.grpc:grpc-protobuf-lite:1.65.1")
    implementation("com.google.protobuf:protobuf-kotlin-lite:3.25.3")

    implementation("com.squareup.okhttp3:okhttp:4.12.0")

    debugImplementation("androidx.compose.ui:ui-tooling")
    debugImplementation("androidx.compose.ui:ui-test-manifest")
}
