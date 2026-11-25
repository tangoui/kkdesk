package com.kkdesk.kkdesk_mobile

import android.os.Build
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyInfo
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel
import java.math.BigInteger
import java.security.KeyFactory
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec

internal class BiometricKey(private val activity: FragmentActivity) {
    private var channel: MethodChannel? = null
    private var activeSigning: MethodChannel.Result? = null
    private var prompt: BiometricPrompt? = null

    fun register(messenger: BinaryMessenger) {
        channel = MethodChannel(messenger, "kkdesk/biometric-key").also {
            it.setMethodCallHandler { call, result ->
                if (call.method !in listOf("prepare", "sign", "remove")) {
                    result.notImplemented()
                    return@setMethodCallHandler
                }
                val args = call.arguments as? Map<*, *>
                val id = args?.get("id") as? String
                val message = args?.get("message") as? ByteArray
                if (id == null || !deviceId.matches(id) || call.method == "sign" && message == null) {
                    result.error("invalid-argument", null, null)
                    return@setMethodCallHandler
                }
                if (activeSigning != null) {
                    result.error("busy", null, null)
                    return@setMethodCallHandler
                }
                val alias = "kkdesk.biometric-key.v1.$id"
                try {
                    val store = KeyStore.getInstance(keyStoreName).apply { load(null) }
                    when (call.method) {
                        "prepare" -> result.success(prepare(store, alias))
                        "sign" -> sign(store, alias, requireNotNull(message), result)
                        "remove" -> {
                            store.deleteEntry(alias)
                            result.success(null)
                        }
                    }
                } catch (error: Exception) {
                    result.error(errorCode(error), null, null)
                }
            }
        }
    }

    fun close() {
        channel?.setMethodCallHandler(null)
        channel = null
        val signing = activeSigning
        activeSigning = null
        prompt?.cancelAuthentication()
        prompt = null
        signing?.error("cancelled", null, null)
    }

    private fun prepare(store: KeyStore, alias: String): ByteArray {
        requireBiometrics()
        val storedKey = store.getKey(alias, null) as? PrivateKey
        if (storedKey != null) {
            requireHardwareProtection(storedKey)
            return encodePoint(store.getCertificate(alias).publicKey as ECPublicKey)
        }

        val generator = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, keyStoreName)
        generator.initialize(keySpec(alias))
        val pair = generator.generateKeyPair()
        try {
            requireHardwareProtection(pair.private)
            return encodePoint(pair.public as ECPublicKey)
        } catch (error: Exception) {
            store.deleteEntry(alias)
            throw error
        }
    }

    private fun sign(
        store: KeyStore,
        alias: String,
        message: ByteArray,
        result: MethodChannel.Result,
    ) {
        val key = store.getKey(alias, null) as? PrivateKey ?: throw Failure("missing")
        requireHardwareProtection(key)
        val signature = Signature.getInstance("SHA256withECDSA").apply { initSign(key) }
        requireBiometrics()
        val promptInfo = BiometricPrompt.PromptInfo.Builder()
            .setTitle("验证生物识别以连接已配对的电脑")
            .setNegativeButtonText("取消")
            .setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG)
            .build()
        val callback = object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(authentication: BiometricPrompt.AuthenticationResult) {
                if (!finishSigning(result)) return
                try {
                    val authenticatedSignature = authentication.cryptoObject?.signature
                        ?: throw Failure("unavailable")
                    authenticatedSignature.update(message)
                    result.success(authenticatedSignature.sign())
                } catch (error: Exception) {
                    result.error(errorCode(error), null, null)
                }
            }

            override fun onAuthenticationError(errorCode: Int, errString: CharSequence) {
                if (!finishSigning(result)) return
                val code = when (errorCode) {
                    BiometricPrompt.ERROR_NEGATIVE_BUTTON,
                    BiometricPrompt.ERROR_USER_CANCELED,
                    BiometricPrompt.ERROR_CANCELED -> "cancelled"
                    else -> "unavailable"
                }
                result.error(code, null, null)
            }
        }
        val signingPrompt = BiometricPrompt(activity, ContextCompat.getMainExecutor(activity), callback)
        activeSigning = result
        prompt = signingPrompt
        try {
            signingPrompt.authenticate(promptInfo, BiometricPrompt.CryptoObject(signature))
        } catch (error: Exception) {
            finishSigning(result)
            throw error
        }
    }

    private fun finishSigning(result: MethodChannel.Result): Boolean {
        if (activeSigning !== result) return false
        activeSigning = null
        prompt = null
        return true
    }

    private fun requireBiometrics() {
        val status = BiometricManager.from(activity)
            .canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG)
        if (status != BiometricManager.BIOMETRIC_SUCCESS) throw Failure("unavailable")
    }

    @Suppress("DEPRECATION")
    private fun keySpec(alias: String): KeyGenParameterSpec {
        val builder = KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256)
            .setUserAuthenticationRequired(true)
            .setInvalidatedByBiometricEnrollment(true)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            builder.setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
        } else {
            builder.setUserAuthenticationValidityDurationSeconds(-1)
        }
        return builder.build()
    }

    @Suppress("DEPRECATION")
    private fun requireHardwareProtection(key: PrivateKey) {
        val factory = KeyFactory.getInstance(key.algorithm, keyStoreName)
        val info = factory.getKeySpec(key, KeyInfo::class.java)
        val inHardware = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            info.securityLevel == KeyProperties.SECURITY_LEVEL_TRUSTED_ENVIRONMENT ||
                info.securityLevel == KeyProperties.SECURITY_LEVEL_STRONGBOX
        } else {
            info.isInsideSecureHardware
        }
        if (!inHardware || !info.isUserAuthenticationRequired ||
            !info.isUserAuthenticationRequirementEnforcedBySecureHardware
        ) {
            throw Failure("unavailable")
        }
    }

    private fun encodePoint(key: ECPublicKey): ByteArray {
        val bytes = ByteArray(65)
        bytes[0] = 4
        writeCoordinate(key.w.affineX, bytes, 1)
        writeCoordinate(key.w.affineY, bytes, 33)
        return bytes
    }

    private fun writeCoordinate(value: BigInteger, bytes: ByteArray, offset: Int) {
        val coordinate = value.toByteArray()
        // BigInteger may prepend a sign byte or omit leading zeroes.
        val start = if (coordinate.size == 33 && coordinate[0] == 0.toByte()) 1 else 0
        val size = coordinate.size - start
        require(value.signum() >= 0 && size <= 32)
        coordinate.copyInto(bytes, offset + 32 - size, start)
    }

    private fun errorCode(error: Exception): String {
        if (error is Failure) return error.code
        if (generateSequence<Throwable>(error) { it.cause }.any { it is KeyPermanentlyInvalidatedException }) {
            return "invalidated"
        }
        return "unavailable"
    }

    private class Failure(val code: String) : Exception()

    private companion object {
        const val keyStoreName = "AndroidKeyStore"
        val deviceId = Regex("[A-Za-z0-9_-]{1,128}")
    }
}
