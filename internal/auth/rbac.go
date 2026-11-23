package auth

// Role rank definitions for hierarchy evaluation.
const (
	RankOwner    = 40
	RankEmployee = 20
	RankCustomer = 10
)

// RoleRank maps each recognized role to its numeric authority rank.
func RoleRank(role string) int {
	switch role {
	case RoleOwner:
		return RankOwner
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
	return role == RoleOwner || role == RoleEmployee
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
// create, promote, or demote roles (including employees and customers).
// Subordinate roles ('employee', 'customer') have zero role management authority.
//
// Rules:
// 1. Only an 'owner' (admin) can create, promote, or demote any role (owner, employee, customer).
// 2. 'employee' and 'customer' have zero role assignment or demotion privileges.
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
// - 'employee' can activate or deactivate 'customer' accounts, but cannot alter other employees or owners.
// - 'customer' has zero status management privileges.
func CanManageUserStatus(actorRole string, targetRole string) bool {
	switch actorRole {
	case RoleOwner:
		return IsValidRole(targetRole)
	case RoleEmployee:
		return targetRole == RoleCustomer
	default:
		return false
	}
}

// CanInspectAuditLogs enforces audit visibility:
//   - 'owner' has full upward visibility over all system audit logs.
//   - 'employee' and 'customer' cannot inspect audit logs.
func CanInspectAuditLogs(actorRole string, targetLogActorRole string) bool {
	return actorRole == RoleOwner
}

// CanAccessUserData enforces Downward Isolation and Lateral Isolation:
// - 'owner' can access any user's profile and sessions.
// - 'employee' cannot inspect or mutate other users' accounts.
// - 'customer' is strictly isolated to their own user record (actorUserID == targetUserID).
func CanAccessUserData(actorRole string, actorUserID string, targetRole string, targetUserID string) bool {
	if actorRole == RoleOwner {
		return true
	}
	if actorUserID != "" && actorUserID == targetUserID {
		return true
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
// Invariant: Only 'owner' has operational and financial exception authority.
func CanReviewPaymentExceptions(actorRole string) bool {
	return actorRole == RoleOwner
}

// CanFulfillOrders checks if the role can fulfill orders.
// Invariant: 'owner' and 'employee' can fulfill orders.
func CanFulfillOrders(actorRole string) bool {
	return IsStaff(actorRole)
}
