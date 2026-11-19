package auth

// Role rank definitions for hierarchy evaluation.
const (
	RankOwner    = 40
	RankManager  = 30
	RankEmployee = 20
	RankCustomer = 10
)

// RoleRank maps each recognized role to its numeric authority rank.
func RoleRank(role string) int {
	switch role {
	case RoleOwner:
		return RankOwner
	case RoleManager:
		return RankManager
	case RoleEmployee:
		return RankEmployee
	case RoleCustomer:
		return RankCustomer
	default:
		return 0
	}
}

// IsValidRole checks whether a role string is a recognized system role.
func IsValidRole(role string) bool {
	return RoleRank(role) > 0
}

// IsStaff reports whether a role belongs to store personnel.
func IsStaff(role string) bool {
	return role == RoleOwner || role == RoleManager || role == RoleEmployee
}

// HasRole checks if the actor's role meets or exceeds the required minimum role rank.
func HasRole(actorRole string, requiredRole string) bool {
	actorRank := RoleRank(actorRole)
	requiredRank := RoleRank(requiredRole)
	if actorRank == 0 || requiredRank == 0 {
		return false
	}
	return actorRank >= requiredRank
}

// CanManageRole enforces that only the 'owner' (apex administrator) has authority to
// create, promote, or demote roles (including managers and employees).
// Subordinate roles ('manager', 'employee', 'customer') have zero role management authority.
//
// Rules:
// 1. Only an 'owner' (admin) can create, promote, or demote any role (owner, manager, employee, customer).
// 2. 'manager', 'employee', and 'customer' have zero role assignment or demotion privileges.
func CanManageRole(actorRole string, targetRole string) bool {
	switch actorRole {
	case RoleOwner:
		// Owner has exclusive apex authority over all role management and demotions
		return IsValidRole(targetRole)
	default:
		return false
	}
}

// CanManageUserStatus enforces Downward Isolation for user account activation and deactivation:
// - 'owner' can activate or deactivate accounts across all roles.
// - 'manager' can activate or deactivate subordinate accounts ('employee' and 'customer').
// - 'employee' and 'customer' have zero status management privileges.
func CanManageUserStatus(actorRole string, targetRole string) bool {
	switch actorRole {
	case RoleOwner:
		return IsValidRole(targetRole)
	case RoleManager:
		return targetRole == RoleEmployee || targetRole == RoleCustomer
	default:
		return false
	}
}

// CanInspectAuditLogs enforces audit visibility:
//   - 'owner' has full upward visibility over all system audit logs.
//   - 'manager' can inspect operational audit events (actions by manager, employee, or customer),
//     but is downward-isolated from owner actions.
//   - 'employee' and 'customer' cannot inspect audit logs.
func CanInspectAuditLogs(actorRole string, targetLogActorRole string) bool {
	switch actorRole {
	case RoleOwner:
		return true
	case RoleManager:
		// Isolated from owner actions; can inspect subordinates and peers
		return targetLogActorRole == RoleManager || targetLogActorRole == RoleEmployee || targetLogActorRole == RoleCustomer
	default:
		return false
	}
}

// CanAccessUserData enforces Downward Isolation and Lateral Isolation:
// - 'owner' can access any user's profile and sessions.
// - 'manager' can access employee and customer user records, but not owner records.
// - 'employee' cannot inspect or mutate other users' accounts.
// - 'customer' is strictly isolated to their own user record (actorUserID == targetUserID).
func CanAccessUserData(actorRole string, actorUserID string, targetRole string, targetUserID string) bool {
	if actorRole == RoleOwner {
		return true
	}
	if actorUserID != "" && actorUserID == targetUserID {
		return true
	}
	if actorRole == RoleManager {
		return targetRole == RoleEmployee || targetRole == RoleCustomer
	}
	return false
}

// CanMutateCatalogPricing checks if the role is allowed to alter base catalog pricing.
// Invariant: Only 'owner' can alter base product pricing formulas.
func CanMutateCatalogPricing(actorRole string) bool {
	return actorRole == RoleOwner
}

// CanApproveOpeningStock checks if the role is allowed to approve inventory opening stock counts.
// Invariant: Only 'owner' can approve initial inventory balances.
func CanApproveOpeningStock(actorRole string) bool {
	return actorRole == RoleOwner
}

// CanReviewPaymentExceptions checks if the role can review and settle payment exceptions.
// Invariant: 'owner' and 'manager' have operational authority.
func CanReviewPaymentExceptions(actorRole string) bool {
	return actorRole == RoleOwner || actorRole == RoleManager
}

// CanFulfillOrders checks if the role can fulfill orders.
// Invariant: 'owner', 'manager', and 'employee' can fulfill orders.
func CanFulfillOrders(actorRole string) bool {
	return IsStaff(actorRole)
}
