import Foundation
import Observation

/// Điều khiển các thao tác GHI PHÁ HUỶ (flash image, format, kiểm tra bad block).
///
/// Tách hẳn khỏi `AppState` (luồng cứu hộ read-mostly) đúng như tầng core: đây là
/// đường phá huỷ toàn ổ, có CoreClient riêng và vòng đời xác nhận riêng.
@MainActor
@Observable
final class ImagingController {
    enum Action: String, CaseIterable, Identifiable {
        case flash, format, wipe, capture, badBlocks
        var id: String { rawValue }
        var title: String {
            switch self {
            case .flash: return "Ghi image"
            case .format: return "Format"
            case .wipe: return "Xoá an toàn"
            case .capture: return "Bắt ảnh"
            case .badBlocks: return "Kiểm tra ổ"
            }
        }
        var icon: String {
            switch self {
            case .flash: return "square.and.arrow.down.on.square"
            case .format: return "eraser"
            case .wipe: return "trash.slash"
            case .capture: return "camera.aperture"
            case .badBlocks: return "stethoscope"
            }
        }
        /// Thao tác có phá dữ liệu không (quyết định có bắt gõ xác nhận).
        /// capture chỉ ĐỌC nên không phá; badBlocks tuỳ chế độ ghi-thử.
        var destructive: Bool {
            switch self {
            case .flash, .format, .wipe: return true
            case .capture, .badBlocks: return false
            }
        }
    }

    enum Phase: Equatable { case idle, ready, running, done, failed }

    // Chọn lựa
    var action: Action = .flash
    var targetBSD: String?
    var target: FlashTarget?

    // Tham số flash
    var imagePath: String = ""
    var verify = true
    // Tham số format
    var fs = "exfat"
    var scheme = "gpt"
    var label = "USB"
    // Tham số bad block
    var writeTest = false
    // Tham số xoá an toàn
    var wipeMethod = "zero" // zero | random | 3pass
    var wipeVerify = true
    // Tham số bắt ảnh
    var capturePath = ""
    var captureCompress = false

    // Xác nhận + trạng thái chạy
    var confirmInput = ""
    var phase: Phase = .idle
    var progress: Double = 0
    var statusText = ""
    var resultText: String?
    var errorText: String?

    private let client = CoreClient()

    /// Đã gõ đúng chuỗi xác nhận ổ chưa (chỉ cần cho thao tác phá huỷ).
    var confirmed: Bool {
        guard let token = target?.confirmToken else { return false }
        return confirmInput == token
    }

    var canRun: Bool {
        guard target != nil, phase != .running else { return false }
        if action == .flash && imagePath.isEmpty { return false }
        if action == .capture && capturePath.isEmpty { return false }
        return needsConfirm ? confirmed : true
    }

    /// Thao tác hiện tại có bắt gõ xác nhận không (phá dữ liệu, hoặc bad-block ghi-thử).
    var needsConfirm: Bool {
        action.destructive || (action == .badBlocks && writeTest)
    }

    /// Lấy thông tin ổ đích để hiển thị và khoá target trước khi thao tác.
    func preflight(bsd: String) async {
        targetBSD = bsd
        target = nil
        confirmInput = ""
        resultText = nil
        errorText = nil
        phase = .idle
        do {
            let r = try await client.call(method: "flash_preflight", params: ["bsd": bsd])
            target = FlashTarget(json: r)
            phase = .ready
        } catch {
            errorText = error.localizedDescription
            phase = .failed
        }
    }

    func run() async {
        guard let t = target else { return }
        phase = .running
        progress = 0
        resultText = nil
        errorText = nil
        do {
            let r: [String: Any]
            switch action {
            case .flash:
                statusText = "Đang ghi image..."
                r = try await call("flash_image", [
                    "bsd": t.bsd, "imagePath": imagePath,
                    "expectSize": t.sizeBytes, "expectModel": t.model,
                    "confirm": confirmInput, "verify": verify,
                ])
                let verified = r["verified"] as? Bool ?? false
                resultText = "Đã ghi \(formatBytes((r["bytesWritten"] as? NSNumber)?.int64Value ?? 0))"
                    + (verified ? " · đã kiểm chứng SHA-256." : ".")
            case .format:
                statusText = "Đang format..."
                r = try await call("format_disk", [
                    "bsd": t.bsd, "fs": fs, "scheme": scheme, "label": label,
                    "expectSize": t.sizeBytes, "expectModel": t.model, "confirm": confirmInput,
                ])
                resultText = "Đã format \(r["fs"] as? String ?? fs) (\(r["scheme"] as? String ?? scheme)), nhãn \(r["label"] as? String ?? label)."
            case .wipe:
                statusText = "Đang xoá an toàn..."
                r = try await call("wipe_disk", [
                    "bsd": t.bsd, "method": wipeMethod, "verify": wipeVerify,
                    "expectSize": t.sizeBytes, "expectModel": t.model, "confirm": confirmInput,
                ])
                let passes = r["passes"] as? Int ?? 1
                let verified = r["verified"] as? Bool ?? false
                resultText = "Đã xoá (\(r["method"] as? String ?? wipeMethod), \(passes) lượt)"
                    + (verified ? " · đã kiểm chứng." : ".")
            case .capture:
                statusText = "Đang bắt ảnh ổ..."
                r = try await call("capture_image", [
                    "bsd": t.bsd, "destPath": capturePath, "compress": captureCompress,
                ])
                let bytes = formatBytes((r["bytesRead"] as? NSNumber)?.int64Value ?? 0)
                resultText = "Đã bắt \(bytes) → \((capturePath as NSString).lastPathComponent)."
            case .badBlocks:
                statusText = writeTest ? "Đang ghi-thử toàn ổ..." : "Đang quét toàn ổ..."
                r = try await call("check_bad_blocks", [
                    "bsd": t.bsd, "write": writeTest,
                    "expectSize": t.sizeBytes, "expectModel": t.model, "confirm": confirmInput,
                ])
                let bad = (r["bad"] as? [[String: Any]] ?? [])
                let scanned = formatBytes((r["bytesScanned"] as? NSNumber)?.int64Value ?? 0)
                resultText = bad.isEmpty
                    ? "Quét \(scanned): không có bad block."
                    : "Quét \(scanned): phát hiện \(bad.count) vùng lỗi."
            }
            phase = .done
        } catch {
            errorText = error.localizedDescription
            phase = .failed
        }
    }

    /// Gọi core kèm cập nhật thanh tiến trình từ event "progress".
    private func call(_ method: String, _ params: [String: Any]) async throws -> [String: Any] {
        try await client.call(method: method, params: params) { event, data in
            guard event == "progress" else { return }
            let done = (data["doneBytes"] as? NSNumber)?.doubleValue ?? 0
            let total = (data["totalBytes"] as? NSNumber)?.doubleValue ?? 0
            Task { @MainActor in
                self.progress = total > 0 ? done / total : 0
            }
        }
    }
}

/// Thông tin ổ đích trả từ `flash_preflight`.
struct FlashTarget {
    let bsd: String
    let model: String
    let sizeBytes: Int64
    let removable: Bool
    let busProtocol: String
    let mountPoints: [String]
    let confirmToken: String

    init?(json: [String: Any]) {
        guard let bsd = json["bsd"] as? String else { return nil }
        self.bsd = bsd
        self.model = json["model"] as? String ?? ""
        self.sizeBytes = (json["sizeBytes"] as? NSNumber)?.int64Value ?? 0
        self.removable = json["removable"] as? Bool ?? false
        self.busProtocol = json["busProtocol"] as? String ?? ""
        self.mountPoints = json["mountPoints"] as? [String] ?? []
        self.confirmToken = json["confirmToken"] as? String ?? bsd
    }
}
