package service

import (
	"fmt"
	"log"
	"sync"
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"
)

type AdminDetail struct {
	Admin models.Admin
	User  *models.User
}

// AdminService 管理员服务
type AdminService struct {
	adminRepo *repository.AdminRepository
	userRepo  *repository.UserRepository

	mu     sync.RWMutex
	admins map[int64]models.AdminRole
	loaded bool
}

func NewAdminService(
	adminRepo *repository.AdminRepository,
	userRepo *repository.UserRepository,
) *AdminService {
	s := &AdminService{
		adminRepo: adminRepo,
		userRepo:  userRepo,
	}
	s.reload()
	return s
}

func (s *AdminService) reload() {
	if s == nil || s.adminRepo == nil {
		return
	}
	admins, err := s.adminRepo.GetAll()
	if err != nil {
		log.Printf("[AdminService] 加载管理员缓存失败: %v", err)
		return
	}
	roles := make(map[int64]models.AdminRole, len(admins))
	for _, admin := range admins {
		roles[admin.UserID] = admin.Role
	}
	s.mu.Lock()
	s.admins = roles
	s.loaded = true
	s.mu.Unlock()
}

func (s *AdminService) remember(userID int64, role models.AdminRole) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return
	}
	if s.admins == nil {
		s.admins = make(map[int64]models.AdminRole)
	}
	s.admins[userID] = role
}

func (s *AdminService) forget(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		return
	}
	delete(s.admins, userID)
}

// IsAuthor 检查用户是否是作者
func (s *AdminService) IsAuthor(userID int64) bool {
	s.mu.RLock()
	loaded := s.loaded
	role, ok := s.admins[userID]
	s.mu.RUnlock()
	if loaded {
		return ok && role == models.RoleAuthor
	}
	isAuthor, err := s.adminRepo.IsAuthor(userID)
	if err != nil {
		log.Printf("[AdminService] Failed to check author status: %v", err)
		return false
	}
	return isAuthor
}

// IsAdmin 检查用户是否是管理员(包括作者和配置管理员)
func (s *AdminService) IsAdmin(userID int64) bool {
	s.mu.RLock()
	loaded := s.loaded
	_, ok := s.admins[userID]
	s.mu.RUnlock()
	if loaded {
		return ok
	}
	isAdmin, err := s.adminRepo.IsAdmin(userID)
	if err != nil {
		log.Printf("[AdminService] Failed to check admin status: %v", err)
		return false
	}
	return isAdmin
}

// AddAdmin 添加管理员(仅作者可用)
func (s *AdminService) AddAdmin(userID int64, role models.AdminRole, createdBy int64) error {
	// 检查创建者是否是作者
	if !s.IsAuthor(createdBy) {
		return fmt.Errorf("无权限:只有作者可以添加管理员")
	}

	// 检查目标用户是否已是管理员
	if s.IsAdmin(userID) {
		return fmt.Errorf("该用户已是管理员")
	}

	// 添加管理员
	if err := s.adminRepo.Add(userID, role, createdBy); err != nil {
		return fmt.Errorf("添加管理员失败: %w", err)
	}
	s.remember(userID, role)

	log.Printf("[AdminService] Admin added: userID=%d, role=%s, by=%d", userID, role, createdBy)
	return nil
}

// RemoveAdmin 移除管理员(仅作者可用)
func (s *AdminService) RemoveAdmin(userID int64, operatorID int64) error {
	// 检查操作者是否是作者
	if !s.IsAuthor(operatorID) {
		return fmt.Errorf("无权限:只有作者可以移除管理员")
	}

	// 检查目标用户是否是作者(作者不能被移除)
	if s.IsAuthor(userID) {
		return fmt.Errorf("不能移除作者")
	}

	// 检查目标用户是否是管理员
	if !s.IsAdmin(userID) {
		return fmt.Errorf("该用户不是管理员")
	}

	// 删除管理员
	if err := s.adminRepo.Delete(userID); err != nil {
		return fmt.Errorf("移除管理员失败: %w", err)
	}
	s.forget(userID)

	log.Printf("[AdminService] Admin removed: userID=%d, by=%d", userID, operatorID)
	return nil
}

// ListAdmins 列出所有管理员
func (s *AdminService) ListAdmins() ([]models.Admin, error) {
	admins, err := s.adminRepo.GetAll()
	if err != nil {
		return nil, fmt.Errorf("获取管理员列表失败: %w", err)
	}
	return admins, nil
}

// CountAdmins 获取管理员总数
func (s *AdminService) CountAdmins() (int64, error) {
	count, err := s.adminRepo.Count()
	if err != nil {
		return 0, fmt.Errorf("获取管理员数量失败: %w", err)
	}
	return count, nil
}

// ListAdminDetails 列出管理员及对应用户信息
func (s *AdminService) ListAdminDetails() ([]AdminDetail, error) {
	admins, err := s.adminRepo.GetAll()
	if err != nil {
		return nil, fmt.Errorf("获取管理员列表失败: %w", err)
	}
	if len(admins) == 0 {
		return []AdminDetail{}, nil
	}

	userIDs := make([]int64, 0, len(admins))
	for _, admin := range admins {
		userIDs = append(userIDs, admin.UserID)
	}

	users, err := s.userRepo.GetByIDs(userIDs)
	if err != nil {
		return nil, fmt.Errorf("批量获取管理员用户信息失败: %w", err)
	}

	userMap := make(map[int64]*models.User, len(users))
	for i := range users {
		user := users[i]
		userMap[user.ID] = &user
	}

	details := make([]AdminDetail, 0, len(admins))
	for _, admin := range admins {
		details = append(details, AdminDetail{
			Admin: admin,
			User:  userMap[admin.UserID],
		})
	}

	return details, nil
}

// GetAdminInfo 获取管理员信息(包括用户详情)
func (s *AdminService) GetAdminInfo(userID int64) (*models.Admin, *models.User, error) {
	admin, err := s.adminRepo.GetByUserID(userID)
	if err != nil {
		return nil, nil, err
	}

	user, err := s.userRepo.GetByID(userID)
	if err != nil {
		return admin, nil, nil // 管理员存在但用户信息未收集
	}

	return admin, user, nil
}

// EnsureAuthor 确保作者存在(用于初始化)
func (s *AdminService) EnsureAuthor(authorID int64) error {
	if err := s.adminRepo.EnsureAuthor(authorID); err != nil {
		return fmt.Errorf("初始化作者失败: %w", err)
	}
	s.remember(authorID, models.RoleAuthor)
	log.Printf("[AdminService] Author ensured: userID=%d", authorID)
	return nil
}
