import Foundation

/// Dữ liệu mẫu để xem trước và chỉnh giao diện mà không cần dịch vụ nền hay ổ
/// thật. Bật bằng biến môi trường DOCTORX_DEMO=1. Không ảnh hưởng chế độ chạy
/// thật: khi tắt, AppState gọi dịch vụ nền như bình thường.
enum DemoData {
    static var isEnabled: Bool {
        ProcessInfo.processInfo.environment["DOCTORX_DEMO"] == "1"
    }

    static func disks() -> [Disk] {
        let raw: [[String: Any]] = [
            [
                "bsd": "disk4", "model": "Kingston DataTraveler 32GB",
                "sizeBytes": 31_000_000_000, "busProtocol": "USB",
                "partitions": [
                    ["bsd": "disk4s1", "label": "KINGSTON", "fs": "fat32",
                     "sizeBytes": 31_000_000_000, "mountPoint": "/Volumes/KINGSTON",
                     "systemPartition": false],
                ],
            ],
            [
                "bsd": "disk5", "model": "Seagate Expansion 4TB",
                "sizeBytes": 4_000_787_030_016, "busProtocol": "USB",
                "partitions": [
                    ["bsd": "disk5s1", "label": "", "fs": "msdos",
                     "sizeBytes": 209_715_200, "mountPoint": "",
                     "systemPartition": true],
                    ["bsd": "disk5s2", "label": "DATA", "fs": "ntfs",
                     "sizeBytes": 3_999_000_000_000, "mountPoint": "/Volumes/DATA",
                     "systemPartition": false],
                ],
            ],
        ]
        return raw.compactMap(Disk.init)
    }

    /// Kết quả quét mẫu cho ổ nhiễm shortcut virus điển hình.
    static func infectedScan() -> ScanResult {
        var r = ScanResult()
        r.usedRawDriver = true
        r.severity = "likely-infected"
        r.concealed = [
            ["path": "/Anh gia dinh", "size": 0, "isDir": true,
             "attrs": ["hidden", "system"], "protected": false],
            ["path": "/Bai tap", "size": 0, "isDir": true,
             "attrs": ["hidden", "system"], "protected": false],
            ["path": "/Hop dong 2026.docx", "size": 284_160, "isDir": false,
             "attrs": ["hidden", "system"], "protected": false],
            ["path": "/anh-the.jpg", "size": 1_872_384, "isDir": false,
             "attrs": ["hidden", "system"], "protected": false],
        ].compactMap(ConcealedItem.init)
        r.findings = [
            ["path": "/Anh gia dinh.lnk", "rule": "lnk-thay-the-thu-muc",
             "reason": "Có shortcut cùng tên trong khi thư mục thật đang bị giấu — đúng cách shortcut virus hoạt động."],
            ["path": "/autorun.inf", "rule": "autorun-inf",
             "reason": "Tệp autorun.inf trỏ tới file thực thi."],
            ["path": "/RECYCLER.exe", "rule": "payload-an-o-goc",
             "reason": "File thực thi bị giấu ngay ở gốc ổ."],
        ].compactMap(WormFinding.init)
        return r
    }

    static func cleanScan() -> ScanResult {
        var r = ScanResult()
        r.usedRawDriver = true
        r.severity = "clean"
        return r
    }
}
