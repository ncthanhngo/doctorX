import SwiftUI

/// Màn hình chào ở vùng chi tiết khi chưa chọn ổ. Tuỳ trạng thái mà hướng dẫn
/// bước tiếp theo cho người dùng.
struct WelcomeView: View {
    let reachable: Bool
    let hasDisks: Bool

    var body: some View {
        VStack(spacing: 20) {
            ZStack {
                Circle().fill(Brand.heroGradient).frame(width: 96, height: 96)
                    .shadow(color: Brand.primary.opacity(0.4), radius: 16, y: 6)
                Image(systemName: "cross.case.fill").font(.system(size: 40, weight: .bold))
                    .foregroundStyle(.white)
            }

            VStack(spacing: 6) {
                Text("DoctorX").font(.largeTitle.bold())
                Text("Tìm và cứu dữ liệu bị virus giấu trên USB, ổ cứng gắn ngoài")
                    .font(.title3).foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }

            if !reachable {
                DaemonSetupCard()
            } else if hasDisks {
                HintRow(icon: "hand.point.left.fill", text: "Chọn một ổ ở danh sách bên trái để bắt đầu quét.")
            } else {
                HintRow(icon: "arrow.down.to.line", text: "Cắm USB hoặc ổ cứng gắn ngoài — DoctorX sẽ tự nhận diện.")
            }

            FeatureRow()
                .padding(.top, 8)
        }
        .padding(40)
        .frame(maxWidth: 560)
    }
}

struct HintRow: View {
    let icon: String
    let text: String
    var body: some View {
        HStack(spacing: 9) {
            Image(systemName: icon).foregroundStyle(Brand.primary)
            Text(text).foregroundStyle(.secondary)
        }
        .font(.callout)
        .padding(.horizontal, 16).padding(.vertical, 11)
        .background(Brand.primary.opacity(0.08), in: Capsule())
    }
}

/// Ba tính năng chính, trình bày như thẻ tính năng.
struct FeatureRow: View {
    var body: some View {
        HStack(spacing: 12) {
            FeatureCard(icon: "eye.fill", tint: Brand.primary,
                        title: "Hiện file ẩn", desc: "Thấy cả file bị đặt Hidden + System")
            FeatureCard(icon: "arrow.down.doc.fill", tint: Brand.accent,
                        title: "Sao chép an toàn", desc: "Cứu dữ liệu ra ổ máy, không đụng ổ gốc")
            FeatureCard(icon: "shield.lefthalf.filled", tint: Brand.warn,
                        title: "Cảnh báo virus", desc: "Nhận diện shortcut virus, autorun")
        }
    }
}

struct FeatureCard: View {
    let icon: String
    let tint: Color
    let title: String
    let desc: String
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Image(systemName: icon).font(.title2).foregroundStyle(tint)
            Text(title).font(.callout.bold())
            Text(desc).font(.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Color(nsColor: .controlBackgroundColor), in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(.primary.opacity(0.06)))
    }
}

/// Thẻ hướng dẫn cài dịch vụ nền khi chưa kết nối được.
struct DaemonSetupCard: View {
    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label("Chưa kết nối được dịch vụ nền", systemImage: "exclamationmark.triangle.fill")
                .font(.headline).foregroundStyle(Brand.warn)
            Text("DoctorX cần một dịch vụ chạy nền (quyền quản trị) để đọc ổ đĩa. Cài một lần:")
                .font(.callout).foregroundStyle(.secondary)
            Text("make install-service")
                .font(.callout.monospaced())
                .padding(.horizontal, 12).padding(.vertical, 8)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(.black.opacity(0.06), in: RoundedRectangle(cornerRadius: 8))
                .textSelection(.enabled)
            Text("hoặc chạy tạm: sudo doctorx-core serve")
                .font(.caption).foregroundStyle(.tertiary).textSelection(.enabled)
        }
        .padding(16)
        .background(Brand.warn.opacity(0.08), in: RoundedRectangle(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).stroke(Brand.warn.opacity(0.25)))
    }
}
