# Stripe Subscription Management - iOS Integration Guide

## Overview

Two new endpoints allow users to view their subscription status and cancel their subscriptions directly from the iOS app.

**Base URL**: `http://68.183.22.205:8080` (Auth Service via Gateway)

---

## 1. Get Subscription Status

### Endpoint
```
GET /user/subscription/status
```

### Headers
```
Authorization: Bearer {jwt_token}
```

### Response - Active Subscription (200 OK)
```json
{
  "account_type": "paid",
  "has_subscription": true,
  "subscription_id": "sub_1Abc123...",
  "subscription_status": "active",
  "current_period_start": "2025-12-01T00:00:00Z",
  "current_period_end": "2026-01-01T00:00:00Z",
  "cancel_at_period_end": false,
  "canceled_at": 0,
  "plan_name": "Premium Monthly",
  "plan_amount": 999,
  "plan_currency": "usd",
  "plan_interval": "month"
}
```

### Response - No Subscription (200 OK)
```json
{
  "account_type": "free",
  "has_subscription": false,
  "subscription_status": "none",
  "message": "No subscription found"
}
```

### Swift Model
```swift
struct SubscriptionStatus: Codable {
    let account_type: String
    let has_subscription: Bool
    let subscription_status: String
    let subscription_id: String?
    let current_period_start: String?
    let current_period_end: String?
    let cancel_at_period_end: Bool?
    let canceled_at: Int?
    let plan_name: String?
    let plan_amount: Int?
    let plan_currency: String?
    let plan_interval: String?
    let message: String?
}
```

### Swift Implementation
```swift
func getSubscriptionStatus(completion: @escaping (Result<SubscriptionStatus, Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401, userInfo: [NSLocalizedDescriptionKey: "No auth token"])))
        return
    }

    let url = URL(string: "http://68.183.22.205:8080/user/subscription/status")!
    var request = URLRequest(url: url)
    request.httpMethod = "GET"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        guard let data = data else {
            completion(.failure(error ?? URLError(.badServerResponse)))
            return
        }

        do {
            let status = try JSONDecoder().decode(SubscriptionStatus.self, from: data)
            completion(.success(status))
        } catch {
            completion(.failure(error))
        }
    }.resume()
}
```

### SwiftUI Usage Example
```swift
import SwiftUI

struct SubscriptionSettingsView: View {
    @State private var subscriptionStatus: SubscriptionStatus?
    @State private var isLoading = false
    @State private var errorMessage: String?

    var body: some View {
        VStack(spacing: 20) {
            if isLoading {
                ProgressView("Loading subscription...")
            } else if let status = subscriptionStatus {
                if status.has_subscription {
                    // Active subscription UI
                    VStack(alignment: .leading, spacing: 10) {
                        Text("Subscription Status")
                            .font(.headline)

                        HStack {
                            Text("Plan:")
                            Spacer()
                            Text(status.plan_name ?? "Premium")
                                .foregroundColor(.blue)
                        }

                        HStack {
                            Text("Status:")
                            Spacer()
                            Text(status.subscription_status.capitalized)
                                .foregroundColor(.green)
                        }

                        if let endDate = status.current_period_end {
                            HStack {
                                Text("Renews:")
                                Spacer()
                                Text(formatDate(endDate))
                            }
                        }

                        if status.cancel_at_period_end == true {
                            Text("⚠️ Subscription will cancel at period end")
                                .font(.caption)
                                .foregroundColor(.orange)
                                .padding(.top, 5)
                        }

                        if let amount = status.plan_amount, let currency = status.plan_currency {
                            HStack {
                                Text("Price:")
                                Spacer()
                                Text("$\(amount / 100) \(currency.uppercased())/\(status.plan_interval ?? "month")")
                            }
                        }
                    }
                    .padding()
                    .background(Color.gray.opacity(0.1))
                    .cornerRadius(10)
                } else {
                    // No subscription UI
                    VStack(spacing: 15) {
                        Text("No Active Subscription")
                            .font(.headline)

                        Text("Upgrade to Premium for unlimited audiobook processing")
                            .font(.caption)
                            .multilineTextAlignment(.center)

                        Button("Upgrade to Premium") {
                            startSubscription()
                        }
                        .buttonStyle(.borderedProminent)
                    }
                    .padding()
                }
            }

            if let error = errorMessage {
                Text(error)
                    .foregroundColor(.red)
                    .font(.caption)
            }
        }
        .padding()
        .onAppear {
            loadSubscriptionStatus()
        }
    }

    func loadSubscriptionStatus() {
        isLoading = true
        errorMessage = nil

        getSubscriptionStatus { result in
            DispatchQueue.main.async {
                isLoading = false
                switch result {
                case .success(let status):
                    self.subscriptionStatus = status
                case .failure(let error):
                    self.errorMessage = error.localizedDescription
                }
            }
        }
    }

    func formatDate(_ isoString: String) -> String {
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: isoString) else { return isoString }

        let displayFormatter = DateFormatter()
        displayFormatter.dateStyle = .medium
        return displayFormatter.string(from: date)
    }

    func startSubscription() {
        // Call existing Stripe checkout session endpoint
        // Implementation from your existing code
    }
}
```

---

## 2. Cancel Subscription

### Endpoint
```
POST /user/subscription/cancel
```

### Headers
```
Authorization: Bearer {jwt_token}
```

### Request Body
None required

### Response - Success (200 OK)
```json
{
  "message": "Subscription canceled successfully",
  "subscription_id": "sub_1Abc123...",
  "cancel_at_period_end": true,
  "current_period_end": "2026-01-01T00:00:00Z",
  "access_until": "2026-01-01T00:00:00Z",
  "info": "Your subscription will remain active until the end of your current billing period"
}
```

### Response - No Subscription (400 Bad Request)
```json
{
  "error": "No active subscription found to cancel"
}
```

### Swift Model
```swift
struct CancelSubscriptionResponse: Codable {
    let message: String
    let subscription_id: String?
    let cancel_at_period_end: Bool?
    let current_period_end: String?
    let access_until: String?
    let info: String?
}
```

### Swift Implementation
```swift
func cancelSubscription(completion: @escaping (Result<CancelSubscriptionResponse, Error>) -> Void) {
    guard let token = KeychainSwift().get("auth_token") else {
        completion(.failure(NSError(domain: "AuthError", code: 401, userInfo: [NSLocalizedDescriptionKey: "No auth token"])))
        return
    }

    let url = URL(string: "http://68.183.22.205:8080/user/subscription/cancel")!
    var request = URLRequest(url: url)
    request.httpMethod = "POST"
    request.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")

    URLSession.shared.dataTask(with: request) { data, response, error in
        guard let data = data else {
            completion(.failure(error ?? URLError(.badServerResponse)))
            return
        }

        // Check HTTP status code
        if let httpResponse = response as? HTTPURLResponse {
            if httpResponse.statusCode == 400 {
                // Parse error message
                if let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                   let errorMsg = json["error"] as? String {
                    completion(.failure(NSError(domain: "SubscriptionError", code: 400, userInfo: [NSLocalizedDescriptionKey: errorMsg])))
                    return
                }
            }
        }

        do {
            let cancelResponse = try JSONDecoder().decode(CancelSubscriptionResponse.self, from: data)
            completion(.success(cancelResponse))
        } catch {
            completion(.failure(error))
        }
    }.resume()
}
```

### SwiftUI Usage Example with Confirmation
```swift
import SwiftUI

struct CancelSubscriptionButton: View {
    @State private var showCancelConfirmation = false
    @State private var showSuccessAlert = false
    @State private var successMessage = ""
    @State private var accessUntilDate = ""
    @State private var errorMessage: String?
    @Binding var subscriptionStatus: SubscriptionStatus?

    var body: some View {
        Button(role: .destructive) {
            showCancelConfirmation = true
        } label: {
            Text("Cancel Subscription")
        }
        .confirmationDialog(
            "Cancel Subscription?",
            isPresented: $showCancelConfirmation,
            titleVisibility: .visible
        ) {
            Button("Yes, Cancel Subscription", role: .destructive) {
                performCancellation()
            }
            Button("Keep Subscription", role: .cancel) { }
        } message: {
            Text("Your subscription will remain active until the end of your current billing period. You can re-subscribe at any time.")
        }
        .alert("Subscription Canceled", isPresented: $showSuccessAlert) {
            Button("OK") {
                // Refresh subscription status
                refreshSubscriptionStatus()
            }
        } message: {
            VStack {
                Text(successMessage)
                if !accessUntilDate.isEmpty {
                    Text("Access until: \(accessUntilDate)")
                }
            }
        }
        .alert("Error", isPresented: .constant(errorMessage != nil)) {
            Button("OK") {
                errorMessage = nil
            }
        } message: {
            if let error = errorMessage {
                Text(error)
            }
        }
    }

    func performCancellation() {
        cancelSubscription { result in
            DispatchQueue.main.async {
                switch result {
                case .success(let response):
                    self.successMessage = response.message
                    self.accessUntilDate = response.access_until ?? ""
                    self.showSuccessAlert = true

                    print("✅ Subscription canceled: \(response.subscription_id ?? "unknown")")

                case .failure(let error):
                    self.errorMessage = error.localizedDescription
                    print("❌ Failed to cancel: \(error.localizedDescription)")
                }
            }
        }
    }

    func refreshSubscriptionStatus() {
        getSubscriptionStatus { result in
            DispatchQueue.main.async {
                if case .success(let status) = result {
                    self.subscriptionStatus = status
                }
            }
        }
    }
}
```

---

## Complete Integration Example

### ViewModel Approach
```swift
import Foundation
import Combine

class SubscriptionViewModel: ObservableObject {
    @Published var subscriptionStatus: SubscriptionStatus?
    @Published var isLoading = false
    @Published var errorMessage: String?

    func loadSubscriptionStatus() {
        isLoading = true
        errorMessage = nil

        getSubscriptionStatus { [weak self] result in
            DispatchQueue.main.async {
                self?.isLoading = false
                switch result {
                case .success(let status):
                    self?.subscriptionStatus = status
                case .failure(let error):
                    self?.errorMessage = error.localizedDescription
                }
            }
        }
    }

    func cancelSubscription(completion: @escaping (Bool) -> Void) {
        cancelSubscription { [weak self] result in
            DispatchQueue.main.async {
                switch result {
                case .success:
                    // Refresh status after cancellation
                    self?.loadSubscriptionStatus()
                    completion(true)
                case .failure(let error):
                    self?.errorMessage = error.localizedDescription
                    completion(false)
                }
            }
        }
    }
}
```

### Full Settings View
```swift
struct SubscriptionManagementView: View {
    @StateObject private var viewModel = SubscriptionViewModel()
    @State private var showCancelConfirmation = false

    var body: some View {
        List {
            Section("Subscription Details") {
                if viewModel.isLoading {
                    ProgressView()
                } else if let status = viewModel.subscriptionStatus {
                    if status.has_subscription {
                        subscriptionDetailsView(status: status)
                    } else {
                        noSubscriptionView()
                    }
                }
            }

            if let status = viewModel.subscriptionStatus,
               status.has_subscription,
               status.cancel_at_period_end != true {
                Section {
                    Button(role: .destructive) {
                        showCancelConfirmation = true
                    } label: {
                        Text("Cancel Subscription")
                    }
                }
            }

            if let error = viewModel.errorMessage {
                Section {
                    Text(error)
                        .foregroundColor(.red)
                        .font(.caption)
                }
            }
        }
        .navigationTitle("Subscription")
        .onAppear {
            viewModel.loadSubscriptionStatus()
        }
        .refreshable {
            viewModel.loadSubscriptionStatus()
        }
        .confirmationDialog(
            "Cancel Subscription?",
            isPresented: $showCancelConfirmation,
            titleVisibility: .visible
        ) {
            Button("Yes, Cancel", role: .destructive) {
                viewModel.cancelSubscription { success in
                    if success {
                        // Show success feedback
                    }
                }
            }
            Button("Keep Subscription", role: .cancel) { }
        } message: {
            Text("You'll keep access until the end of your billing period")
        }
    }

    @ViewBuilder
    func subscriptionDetailsView(status: SubscriptionStatus) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            LabeledContent("Status", value: status.subscription_status.capitalized)

            if let plan = status.plan_name {
                LabeledContent("Plan", value: plan)
            }

            if let endDate = status.current_period_end {
                LabeledContent("Renews", value: formatDate(endDate))
            }

            if status.cancel_at_period_end == true {
                Label("Canceling at period end", systemImage: "exclamationmark.triangle.fill")
                    .foregroundColor(.orange)
                    .font(.caption)
            }
        }
    }

    @ViewBuilder
    func noSubscriptionView() -> some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("No active subscription")
                .font(.headline)

            Button("Upgrade to Premium") {
                // Navigate to upgrade flow
            }
            .buttonStyle(.borderedProminent)
        }
    }

    func formatDate(_ isoString: String) -> String {
        let formatter = ISO8601DateFormatter()
        guard let date = formatter.date(from: isoString) else { return isoString }

        let displayFormatter = DateFormatter()
        displayFormatter.dateStyle = .medium
        return displayFormatter.string(from: date)
    }
}
```

---

## Important Notes

### 1. Cancellation Behavior
- **Immediate effect**: Subscription is marked as `cancel_at_period_end = true`
- **Access retention**: User keeps Premium access until `current_period_end`
- **Automatic downgrade**: Stripe webhook downgrades account to "free" when period expires
- **Re-subscription**: User can re-subscribe at any time

### 2. Subscription Status Fields
- `subscription_status`: Can be "active", "trialing", "inactive", "none"
- `cancel_at_period_end`: `true` if cancellation is scheduled
- `plan_amount`: Price in cents (999 = $9.99)
- `plan_interval`: "month" or "year"

### 3. Error Handling
Common error scenarios:
- **401 Unauthorized**: Token missing or invalid → Redirect to login
- **400 Bad Request**: No subscription to cancel → Hide cancel button
- **500 Server Error**: Stripe API issue → Show retry option

### 4. UI/UX Best Practices
- Show subscription status prominently in settings
- Display renewal date clearly
- Warn users about cancellation consequences
- Show "Access until [date]" after cancellation
- Use confirmation dialogs for destructive actions
- Refresh status after successful operations

### 5. Testing
Use Stripe test mode:
- Test card: `4242 4242 4242 4242`
- Any future expiry date
- Any 3-digit CVC
- Test webhook events in Stripe Dashboard

---

## API Flow Diagram

```
User Opens Settings
       ↓
GET /user/subscription/status
       ↓
   Has Subscription?
    ↙         ↘
  Yes          No
   ↓            ↓
Show Details   Show Upgrade Button
   ↓
User Clicks "Cancel"
   ↓
Confirmation Dialog
   ↓
POST /user/subscription/cancel
   ↓
Success Response
   ↓
Show "Access until [date]"
   ↓
Refresh Status (shows cancel_at_period_end: true)
   ↓
Period Ends
   ↓
Stripe Webhook → Account downgraded to "free"
```

---

## Dependencies

Add these to your project:
```swift
// Keychain for secure token storage
import KeychainSwift

// URLSession is built-in, no external dependency needed
```

Install via SPM:
```
https://github.com/evgenyneu/keychain-swift
```

---

## Complete Code Files

You now have everything needed to integrate subscription management:

1. **Data Models**: `SubscriptionStatus`, `CancelSubscriptionResponse`
2. **API Functions**: `getSubscriptionStatus()`, `cancelSubscription()`
3. **ViewModel**: `SubscriptionViewModel` (optional but recommended)
4. **UI Components**: Complete SwiftUI views with all states
5. **Error Handling**: Comprehensive error scenarios covered

Deploy these to your iOS app and users will have full control over their subscriptions!