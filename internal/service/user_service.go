package service

import (
	"telegram-ban-bot/internal/database/models"
	"telegram-ban-bot/internal/database/repository"

	"github.com/mymmrac/telego"
)

// UserService 用户服务
type UserService struct {
	userRepo *repository.UserRepository
}

func NewUserService(userRepo *repository.UserRepository) *UserService {
	return &UserService{
		userRepo: userRepo,
	}
}

// CreateOrUpdateFromMessage 从消息创建或更新用户
func (s *UserService) CreateOrUpdateFromMessage(message *telego.Message) error {
	if message.From == nil {
		return nil
	}

	user := &models.User{
		ID:        message.From.ID,
		Username:  message.From.Username,
		FirstName: message.From.FirstName,
		LastName:  message.From.LastName,
	}

	return s.userRepo.CreateOrUpdate(user)
}

// GetUser 获取用户信息
func (s *UserService) GetUser(userID int64) (*models.User, error) {
	return s.userRepo.GetByID(userID)
}

// CreateOrUpdateUser 创建或更新用户
func (s *UserService) CreateOrUpdateUser(userID int64, username, firstName, lastName string) error {
	user := &models.User{
		ID:        userID,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
	}
	return s.userRepo.CreateOrUpdate(user)
}

// TouchActivity 资料未变化时只刷新活跃时间。没有对应用户行时返回 false。
func (s *UserService) TouchActivity(userID int64) (bool, error) {
	if s == nil || s.userRepo == nil {
		return false, nil
	}
	return s.userRepo.TouchActivity(userID)
}

// CreateOrUpdateFromUser 从 telego.User 创建或更新用户
func (s *UserService) CreateOrUpdateFromUser(user *telego.User) error {
	if user == nil {
		return nil
	}

	u := &models.User{
		ID:        user.ID,
		Username:  user.Username,
		FirstName: user.FirstName,
		LastName:  user.LastName,
	}

	return s.userRepo.CreateOrUpdate(u)
}

// CreateOrUpdateFromChat 从 telego.Chat 创建或更新用户(用于通过用户名查询的场景)
func (s *UserService) CreateOrUpdateFromChat(chat *telego.Chat) error {
	if chat == nil || chat.Type != "private" {
		return nil
	}

	u := &models.User{
		ID:        chat.ID,
		Username:  chat.Username,
		FirstName: chat.FirstName,
		LastName:  chat.LastName,
	}

	return s.userRepo.CreateOrUpdate(u)
}

// CreateOrUpdateFromChatFullInfo 从 telego.ChatFullInfo 创建或更新用户
func (s *UserService) CreateOrUpdateFromChatFullInfo(chat *telego.ChatFullInfo) error {
	if chat == nil || chat.Type != "private" {
		return nil
	}

	u := &models.User{
		ID:        chat.ID,
		Username:  chat.Username,
		FirstName: chat.FirstName,
		LastName:  chat.LastName,
	}

	return s.userRepo.CreateOrUpdate(u)
}
