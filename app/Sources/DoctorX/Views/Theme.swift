import SwiftUI

/// Hệ màu và thành phần giao diện dùng chung, để toàn app trông nhất quán và
/// chuyên nghiệp. Bảng màu theo hướng "y tế / cứu hộ": xanh teal tin cậy làm
/// màu thương hiệu, và ba màu ngữ nghĩa rõ ràng cho trạng thái an toàn / nghi
/// ngờ / nguy hiểm.
enum Brand {
    static let primary = Color(hex: 0x0D9488)   // teal-600
    static let primaryDeep = Color(hex: 0x0F766E) // teal-700
    static let accent = Color(hex: 0x0891B2)    // cyan-600

    static let safe = Color(hex: 0x16A34A)      // green-600
    static let warn = Color(hex: 0xF59E0B)      // amber-500
    static let danger = Color(hex: 0xDC2626)    // red-600

    /// Gradient thương hiệu cho header và điểm nhấn.
    static var heroGradient: LinearGradient {
        LinearGradient(
            colors: [primary, accent],
            startPoint: .topLeading, endPoint: .bottomTrailing
        )
    }

    static func statusGradient(_ tint: Color) -> LinearGradient {
        LinearGradient(colors: [tint.opacity(0.9), tint], startPoint: .top, endPoint: .bottom)
    }
}

extension Color {
    /// Khởi tạo từ mã hex kiểu 0xRRGGBB.
    init(hex: UInt32) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: 1
        )
    }
}

/// Màu và biểu tượng theo mức độ nghiêm trọng của kết quả quét.
struct SeverityStyle {
    let tint: Color
    let icon: String
    let title: String

    init(severity: String) {
        switch severity {
        case "likely-infected":
            tint = Brand.danger
            icon = "exclamationmark.octagon.fill"
            title = "Nhiều khả năng đã nhiễm"
        case "suspicious":
            tint = Brand.warn
            icon = "exclamationmark.triangle.fill"
            title = "Có dấu hiệu đáng ngờ"
        default:
            tint = Brand.safe
            icon = "checkmark.shield.fill"
            title = "Không thấy dấu hiệu bất thường"
        }
    }
}

/// Huy hiệu trạng thái hình viên thuốc, dùng cho pill "an toàn / nghi ngờ".
struct StatusPill: View {
    let text: String
    let tint: Color
    var icon: String? = nil

    var body: some View {
        HStack(spacing: 6) {
            if let icon { Image(systemName: icon) }
            Text(text).fontWeight(.semibold)
        }
        .font(.callout)
        .foregroundStyle(.white)
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Brand.statusGradient(tint), in: Capsule())
    }
}

/// Nhãn loại filesystem, tô màu để phân biệt nhanh.
struct FSBadge: View {
    let fs: String

    private var color: Color {
        switch fs.lowercased() {
        case "ntfs": return Color(hex: 0x7C3AED)   // tím
        case "exfat": return Color(hex: 0x0891B2)  // cyan
        case "fat32", "fat16", "fat12": return Color(hex: 0x2563EB) // xanh dương
        case "apfs", "hfs": return Color(hex: 0x64748B) // xám xanh (của Mac)
        default: return Color(hex: 0x64748B)
        }
    }

    var body: some View {
        Text(fs.isEmpty ? "—" : fs.uppercased())
            .font(.caption2.weight(.bold))
            .foregroundStyle(color)
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color.opacity(0.14), in: RoundedRectangle(cornerRadius: 5))
    }
}

/// Thanh dung lượng đã dùng — trực quan cho ổ đĩa.
struct CapacityBar: View {
    let usedFraction: Double
    var tint: Color = Brand.primary

    var body: some View {
        GeometryReader { geo in
            ZStack(alignment: .leading) {
                Capsule().fill(Color.primary.opacity(0.08))
                Capsule().fill(tint.opacity(0.85))
                    .frame(width: max(4, geo.size.width * min(1, max(0, usedFraction))))
            }
        }
        .frame(height: 6)
    }
}

/// Thẻ thống kê nhỏ: một con số nổi bật + nhãn.
struct StatCard: View {
    let value: String
    let label: String
    let icon: String
    var tint: Color = Brand.primary

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                RoundedRectangle(cornerRadius: 10).fill(tint.opacity(0.15))
                Image(systemName: icon).foregroundStyle(tint).font(.title3)
            }
            .frame(width: 40, height: 40)

            VStack(alignment: .leading, spacing: 1) {
                Text(value).font(.title2.bold()).foregroundStyle(.primary)
                Text(label).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
        }
        .padding(14)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.primary.opacity(0.06)))
    }
}

/// Nút hành động chính, nổi bật với gradient thương hiệu.
struct BrandButton: View {
    let title: String
    let icon: String
    var tint: Color = Brand.primary
    var filled: Bool = true
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 7) {
                Image(systemName: icon)
                Text(title).fontWeight(.semibold)
            }
            .font(.callout)
            .padding(.horizontal, 16).padding(.vertical, 10)
            .frame(maxWidth: .infinity)
            .foregroundStyle(filled ? .white : tint)
            .background {
                if filled {
                    Brand.statusGradient(tint)
                } else {
                    tint.opacity(0.12)
                }
            }
            .clipShape(RoundedRectangle(cornerRadius: 10))
        }
        .buttonStyle(.plain)
    }
}

/// Khối nội dung có tiêu đề, bo góc, dùng làm section trong màn chi tiết.
struct SectionCard<Content: View>: View {
    let title: String
    var systemImage: String? = nil
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                if let systemImage { Image(systemName: systemImage).foregroundStyle(Brand.primary) }
                Text(title).font(.headline)
            }
            content()
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(.primary.opacity(0.06)))
    }
}

/// Định dạng số byte gọn.
func formatBytes(_ n: Int64) -> String {
    let f = ByteCountFormatter()
    f.countStyle = .file
    return f.string(fromByteCount: n)
}
