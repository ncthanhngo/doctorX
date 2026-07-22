import Foundation
import Observation

/// Điều khiển các tính năng CHẨN ĐOÁN (chỉ đọc): SMART health, phát hiện mã hoá,
/// nhật ký thao tác. Không phá dữ liệu nên không có vòng xác nhận.
@MainActor
@Observable
final class DiagnosticsController {
    var health: DriveHealth?
    var healthLoading = false
    var healthError: String?

    /// Mã hoá theo từng phân vùng: bsd → nhãn loại ("bitlocker"/"filevault"/...).
    var encryption: [String: String] = [:]

    var history: [HistoryRow] = []

    private let client = CoreClient()

    func loadHealth(bsd: String) async {
        healthLoading = true
        healthError = nil
        health = nil
        defer { healthLoading = false }
        do {
            let r = try await client.call(method: "drive_health", params: ["bsd": bsd])
            health = DriveHealth(json: r)
        } catch {
            healthError = error.localizedDescription
        }
    }

    func detectEncryption(bsd: String) async {
        do {
            let r = try await client.call(method: "detect_encryption", params: ["bsd": bsd])
            encryption[bsd] = r["encryption"] as? String ?? "none"
        } catch {
            // Chẩn đoán là gợi ý — lỗi thì bỏ qua, không làm phiền.
        }
    }

    func loadHistory(limit: Int = 100) async {
        do {
            let r = try await client.call(method: "list_history", params: ["limit": limit])
            history = (r["records"] as? [[String: Any]] ?? []).compactMap(HistoryRow.init)
        } catch {
            history = []
        }
    }
}

/// Kết quả SMART một ổ.
struct DriveHealth {
    let available: Bool
    let passed: Bool
    let model: String
    let serial: String
    let temperatureC: Int
    let powerOnHours: Int
    let reallocatedSectors: Int
    let note: String

    init(json: [String: Any]) {
        self.available = json["available"] as? Bool ?? false
        self.passed = json["passed"] as? Bool ?? false
        self.model = json["model"] as? String ?? ""
        self.serial = json["serial"] as? String ?? ""
        self.temperatureC = json["temperatureC"] as? Int ?? 0
        self.powerOnHours = json["powerOnHours"] as? Int ?? 0
        self.reallocatedSectors = json["reallocatedSectors"] as? Int ?? 0
        self.note = json["note"] as? String ?? ""
    }
}

/// Một dòng nhật ký thao tác.
struct HistoryRow: Identifiable {
    let time: String
    let op: String
    let device: String
    let model: String
    let result: String
    let detail: String

    var id: String { time + op + device }

    init?(json: [String: Any]) {
        guard let op = json["op"] as? String else { return nil }
        self.op = op
        self.time = json["time"] as? String ?? ""
        self.device = json["device"] as? String ?? ""
        self.model = json["model"] as? String ?? ""
        self.result = json["result"] as? String ?? ""
        self.detail = json["detail"] as? String ?? ""
    }
}
