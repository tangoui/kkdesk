package com.kkdesk.kkdesk_mobile

import android.graphics.BitmapFactory
import android.net.Uri
import android.provider.MediaStore
import android.provider.OpenableColumns
import android.webkit.MimeTypeMap
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import io.flutter.embedding.android.FlutterFragmentActivity
import io.flutter.plugin.common.BinaryMessenger
import io.flutter.plugin.common.MethodChannel
import java.io.File
import java.io.IOException
import java.util.concurrent.Executors

internal class MediaUploadPicker(private val activity: FlutterFragmentActivity) {
    private val worker = Executors.newSingleThreadExecutor()
    private var pending: MethodChannel.Result? = null
    private val picker = activity.registerForActivityResult(
        ActivityResultContracts.PickMultipleVisualMedia()
    ) { items ->
        val reply = pending ?: return@registerForActivityResult
        if (items.isEmpty()) {
            pending = null
            reply.success(emptyList<Map<String, Any?>>())
            return@registerForActivityResult
        }

        val importedAt = System.currentTimeMillis()
        worker.execute {
            val exported = mutableListOf<Map<String, Any?>>()
            try {
                for (item in items) exported.add(export(item, importedAt))
                activity.runOnUiThread {
                    pending = null
                    reply.success(exported)
                }
            } catch (_: Exception) {
                for (item in exported) File(item.getValue("path") as String).delete()
                activity.runOnUiThread {
                    pending = null
                    reply.error("media_export", "Unable to export selected media", null)
                }
            }
        }
    }

    fun register(messenger: BinaryMessenger) {
        MethodChannel(messenger, "kkdesk/media_upload").setMethodCallHandler { call, result ->
            when {
                call.method != "pick" -> result.notImplemented()
                pending != null -> result.error("media_picker_busy", "Picker already open", null)
                else -> {
                    pending = result
                    try {
                        picker.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageAndVideo))
                    } catch (_: Exception) {
                        pending = null
                        result.error("media_picker", "Unable to open the media picker", null)
                    }
                }
            }
        }
    }

    fun close() {
        worker.shutdown()
    }

    private fun export(uri: Uri, importedAt: Long): Map<String, Any?> {
        val resolver = activity.contentResolver
        val name = attribute(uri, OpenableColumns.DISPLAY_NAME)
        // Some picker providers supply the URI's numeric ID as a display name.
        val mediaId = uri.lastPathSegment?.takeIf { it.toLongOrNull() != null }
        val isOriginalName = name != null && name.substringBeforeLast('.') != mediaId
        val reportedMimeType = resolver.getType(uri)
        val timestamp = epochMillis(attribute(uri, MediaStore.MediaColumns.DATE_TAKEN))
            ?: epochMillis(attribute(uri, MediaStore.MediaColumns.DATE_ADDED))
            ?: importedAt
        val folder = File(activity.cacheDir, "media-uploads").apply { mkdirs() }
        val copy = File.createTempFile("upload-", ".media", folder)
        try {
            val input = resolver.openInputStream(uri) ?: throw IOException("Media stream unavailable")
            input.use { stream -> copy.outputStream().use { stream.copyTo(it) } }
            val mimeType = reportedMimeType?.takeIf { it.startsWith("image/") || it.startsWith("video/") }
                ?: BitmapFactory.Options().apply {
                    inJustDecodeBounds = true
                    BitmapFactory.decodeFile(copy.path, this)
                }.outMimeType
            val types = MimeTypeMap.getSingleton()
            val namedExtension = name?.substringAfterLast('.', "") ?: ""
            val video = mimeType?.startsWith("video/") == true ||
                types.getMimeTypeFromExtension(namedExtension.lowercase())?.startsWith("video/") == true
            val extension = types.getExtensionFromMimeType(mimeType) ?: namedExtension
            return mapOf(
                "path" to copy.path, "name" to name, "extension" to extension,
                "isOriginalName" to isOriginalName,
                "video" to video, "timestampMs" to timestamp
            )
        } catch (error: Exception) {
            copy.delete()
            throw error
        }
    }

    private fun attribute(uri: Uri, column: String): String? = try {
        activity.contentResolver.query(uri, arrayOf(column), null, null, null)?.use { cursor ->
            val index = cursor.getColumnIndex(column)
            if (index >= 0 && cursor.moveToFirst() && !cursor.isNull(index)) cursor.getString(index) else null
        }
    } catch (_: Exception) {
        null
    }

    private fun epochMillis(value: String?): Long? {
        val time = value?.toLongOrNull()?.takeIf { it > 0 } ?: return null
        return if (time < 100_000_000_000L) time * 1000 else time
    }
}
