import Foundation

/// Một phân vùng có thể thao tác.
struct Partition: Identifiable, Hashable {
    let bsd: String
    let label: String
    let fsType: String
    let sizeBytes: Int64
    let mountPoint: String
    /// Phân vùng hệ thống (EFI, Recovery...). Mọi thao tác ghi bị chặn.
    let isSystem: Bool

    var id: String { bsd }

    var displayName: String {
        label.isEmpty ? "(không nhãn)" : label
    }

    init?(json: [String: Any]) {
        guard let bsd = json["bsd"] as? String else { return nil }
        self.bsd = bsd
        self.label = json["label"] as? String ?? ""
        self.fsType = json["fs"] as? String ?? ""
        self.sizeBytes = (json["sizeBytes"] as? NSNumber)?.int64Value ?? 0
        self.mountPoint = json["mountPoint"] as? String ?? ""
        self.isSystem = json["systemPartition"] as? Bool ?? false
    }
}

/// Một ổ vật lý gắn ngoài.
struct Disk: Identifiable, Hashable {
    let bsd: String
    let model: String
    let sizeBytes: Int64
    let busProtocol: String
    let partitions: [Partition]

    var id: String { bsd }

    init?(json: [String: Any]) {
        guard let bsd = json["bsd"] as? String else { return nil }
        self.bsd = bsd
        self.model = json["model"] as? String ?? "Ổ gắn ngoài"
        self.sizeBytes = (json["sizeBytes"] as? NSNumber)?.int64Value ?? 0
        self.busProtocol = json["busProtocol"] as? String ?? ""
        self.partitions = (json["partitions"] as? [[String: Any]] ?? []).compactMap(Partition.init)
    }
}

/// Một mục bị giấu khỏi tầm nhìn người dùng.
struct ConcealedItem: Identifiable, Hashable {
    let path: String
    let size: Int64
    let isDir: Bool
    let attrs: [String]
    /// Mục hệ thống hoặc file phụ trợ của macOS — không phải dữ liệu người dùng.
    let isProtected: Bool

    var id: String { path }

    var name: String {
        (path as NSString).lastPathComponent
    }

    init?(json: [String: Any]) {
        guard let path = json["path"] as? String else { return nil }
        self.path = path
        self.size = (json["size"] as? NSNumber)?.int64Value ?? 0
        self.isDir = json["isDir"] as? Bool ?? false
        self.attrs = json["attrs"] as? [String] ?? []
        self.isProtected = json["protected"] as? Bool ?? false
    }
}

/// Một dấu hiệu nghi ngờ tìm được.
struct WormFinding: Identifiable, Hashable {
    let path: String
    let rule: String
    let reason: String

    var id: String { "\(rule):\(path)" }

    init?(json: [String: Any]) {
        guard let path = json["path"] as? String else { return nil }
        self.path = path
        self.rule = json["rule"] as? String ?? ""
        self.reason = json["reason"] as? String ?? ""
    }
}

/// Kết quả quét một phân vùng.
struct ScanResult {
    var concealed: [ConcealedItem] = []
    var findings: [WormFinding] = []
    var severity: String = "clean"
    /// Sai khi phải quét qua mount vì chưa có driver raw cho filesystem này —
    /// khi đó không phát hiện được bit System.
    var usedRawDriver: Bool = true

    /// Chỉ những mục thực sự là dữ liệu người dùng.
    var userData: [ConcealedItem] {
        concealed.filter { !$0.isProtected }
    }

    var severityLabel: String {
        switch severity {
        case "likely-infected": return "Nhiều khả năng đã nhiễm"
        case "suspicious": return "Có dấu hiệu đáng ngờ"
        default: return "Không thấy dấu hiệu bất thường"
        }
    }
}

func formatBytes(_ n: Int64) -> String {
    let f = ByteCountFormatter()
    f.countStyle = .file
    return f.string(fromByteCount: n)
}
