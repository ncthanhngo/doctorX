import Foundation

/// Kết nối tới doctorx-core qua Unix domain socket.
///
/// Giao thức NDJSON: mỗi dòng một thông điệp JSON. Một yêu cầu có thể nhận
/// nhiều `event` báo tiến trình trước khi tới phản hồi cuối cùng.
actor CoreClient {
    enum ClientError: LocalizedError {
        case notConnected
        case daemonError(code: String, message: String)
        case badResponse(String)

        var errorDescription: String? {
            switch self {
            case .notConnected:
                return "Chưa kết nối được tới dịch vụ nền của DoctorX."
            case .daemonError(_, let message):
                return message
            case .badResponse(let detail):
                return "Phản hồi không hợp lệ từ dịch vụ nền: \(detail)"
            }
        }
    }

    private let socketPath: String
    private var fd: Int32 = -1
    private var readBuffer = Data()
    private var nextID = 1

    init(socketPath: String = "/var/run/doctorx.sock") {
        self.socketPath = socketPath
    }

    var isConnected: Bool { fd >= 0 }

    func connect() throws {
        if fd >= 0 { return }

        let sock = socket(AF_UNIX, SOCK_STREAM, 0)
        guard sock >= 0 else { throw ClientError.notConnected }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8)
        guard pathBytes.count < MemoryLayout.size(ofValue: addr.sun_path) else {
            close(sock)
            throw ClientError.notConnected
        }
        withUnsafeMutablePointer(to: &addr.sun_path) { ptr in
            ptr.withMemoryRebound(to: CChar.self, capacity: pathBytes.count + 1) { dst in
                for (i, b) in pathBytes.enumerated() { dst[i] = CChar(bitPattern: b) }
                dst[pathBytes.count] = 0
            }
        }

        let size = socklen_t(MemoryLayout<sockaddr_un>.size)
        let ok = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                Darwin.connect(sock, $0, size)
            }
        }
        guard ok == 0 else {
            close(sock)
            throw ClientError.notConnected
        }
        fd = sock
    }

    func disconnect() {
        if fd >= 0 { close(fd) }
        fd = -1
        readBuffer.removeAll()
    }

    /// Gửi một yêu cầu và trả về kết quả cuối cùng.
    ///
    /// `onEvent` được gọi cho mỗi thông điệp tiến trình nhận được trong lúc chờ.
    func call(
        method: String,
        params: [String: Any] = [:],
        onEvent: (@Sendable (String, [String: Any]) -> Void)? = nil
    ) throws -> [String: Any] {
        try connect()

        let id = nextID
        nextID += 1
        let payload: [String: Any] = ["id": id, "method": method, "params": params]
        var line = try JSONSerialization.data(withJSONObject: payload)
        line.append(0x0A)
        try writeAll(line)

        // Đọc tới khi gặp phản hồi cuối cùng của đúng id này.
        while true {
            let msg = try readLine()
            guard let msgID = msg["id"] as? Int, msgID == id else { continue }

            if let event = msg["event"] as? String {
                onEvent?(event, msg["data"] as? [String: Any] ?? [:])
                continue
            }
            if let ok = msg["ok"] as? Bool, ok {
                return msg["result"] as? [String: Any] ?? [:]
            }
            let err = msg["error"] as? [String: Any] ?? [:]
            throw ClientError.daemonError(
                code: err["code"] as? String ?? "unknown",
                message: err["message"] as? String ?? "Lỗi không rõ."
            )
        }
    }

    private func writeAll(_ data: Data) throws {
        var sent = 0
        try data.withUnsafeBytes { (raw: UnsafeRawBufferPointer) in
            let base = raw.bindMemory(to: UInt8.self).baseAddress!
            while sent < data.count {
                let n = write(fd, base + sent, data.count - sent)
                if n <= 0 {
                    disconnect()
                    throw ClientError.notConnected
                }
                sent += n
            }
        }
    }

    private func readLine() throws -> [String: Any] {
        while true {
            if let idx = readBuffer.firstIndex(of: 0x0A) {
                let lineData = readBuffer[readBuffer.startIndex..<idx]
                readBuffer.removeSubrange(readBuffer.startIndex...idx)
                guard let obj = try? JSONSerialization.jsonObject(with: lineData),
                      let dict = obj as? [String: Any]
                else {
                    throw ClientError.badResponse(String(decoding: lineData, as: UTF8.self))
                }
                return dict
            }

            var chunk = [UInt8](repeating: 0, count: 64 * 1024)
            let n = read(fd, &chunk, chunk.count)
            if n <= 0 {
                disconnect()
                throw ClientError.notConnected
            }
            readBuffer.append(contentsOf: chunk[0..<n])
        }
    }
}
