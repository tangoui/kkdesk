package com.kkdesk.kkdesk_mobile

import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterFragmentActivity() {
    private val mediaUploadPicker = MediaUploadPicker(this)

    override fun onDestroy() {
        mediaUploadPicker.close()
        super.onDestroy()
    }

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        mediaUploadPicker.register(flutterEngine.dartExecutor.binaryMessenger)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "kkdesk/display")
            .setMethodCallHandler { call, result ->
                if (call.method != "setImmersive") {
                    result.notImplemented()
                    return@setMethodCallHandler
                }

                // Hide the bars while keeping the edge-to-edge window on Android 16.
                val controller = WindowCompat.getInsetsController(window, window.decorView)
                if (call.arguments == true) {
                    controller.systemBarsBehavior =
                        WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                    controller.hide(WindowInsetsCompat.Type.systemBars())
                } else {
                    controller.show(WindowInsetsCompat.Type.systemBars())
                }
                result.success(null)
            }
    }
}
