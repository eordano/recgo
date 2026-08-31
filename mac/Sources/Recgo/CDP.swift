import Foundation

// Browser and Tab modes attach to a running browser's CDP port (recgo's
// default, 9222). The probe hits /json/list — the same endpoint the
// recorders use — so "available" means a session would actually start.
final class CDP: ObservableObject {
    static let shared = CDP()

    @Published var available = false

    func refresh(_ done: ((Bool) -> Void)? = nil) {
        var req = URLRequest(url: URL(string: "http://127.0.0.1:9222/json/list")!)
        req.timeoutInterval = 0.6
        URLSession.shared.dataTask(with: req) { data, res, _ in
            let up = (res as? HTTPURLResponse)?.statusCode == 200 && data != nil
            DispatchQueue.main.async { [weak self] in
                self?.available = up
                done?(up)
            }
        }.resume()
    }
}
