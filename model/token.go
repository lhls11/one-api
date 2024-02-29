package model

import (
	"errors"
	"fmt"

	"github.com/songquanpeng/one-api/common"
	"github.com/songquanpeng/one-api/common/config"
	"github.com/songquanpeng/one-api/common/helper"
	"github.com/songquanpeng/one-api/common/logger"
	"gorm.io/gorm"
)

type Token struct {
	Id                   int    `json:"id"`
	UserId               int    `json:"user_id"`
	Key                  string `json:"key" gorm:"type:char(48);uniqueIndex"`
	Status               int    `json:"status" gorm:"default:1"`
	Name                 string `json:"name" gorm:"index" `
	CreatedTime          int64  `json:"created_time" gorm:"bigint"`
	AccessedTime         int64  `json:"accessed_time" gorm:"bigint"`
	ExpiredTime          int64  `json:"expired_time" gorm:"bigint;default:-1"` // -1 means never expired
	RemainQuota          int    `json:"remain_quota" gorm:"default:0"`
	UnlimitedQuota       bool   `json:"unlimited_quota" gorm:"default:false"`
	UsedQuota            int    `json:"used_quota" gorm:"default:0"` // used quota
	TokenRemindThreshold int    `json:"token_remind_threshold"`
}

func GetAllUserTokens(userId int, startIdx int, num int) ([]*Token, error) {
	var tokens []*Token
	var err error
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(num).Offset(startIdx).Find(&tokens).Error
	return tokens, err
}

func GetUserTokensAndCount(userId int, page int, pageSize int) (tokens []*Token, total int64, err error) {
	// 首先计算特定用户的令牌总数
	err = DB.Model(&Token{}).Where("user_id = ?", userId).Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算起始索引，基于page和pageSize。第一页的起始索引为0。
	offset := (page - 1) * pageSize

	// 获取当前页面的用户令牌列表
	err = DB.Where("user_id = ?", userId).Order("id desc").Limit(pageSize).Offset(offset).Find(&tokens).Error
	if err != nil {
		return nil, total, err
	}

	// 返回用户令牌列表、总数以及可能的错误信息
	return tokens, total, nil
}

func SearchUserTokensAndCount(userId int, keyword string, page int, pageSize int, status *int) (tokens []*Token, total int64, err error) {
	// 用于LIKE查询的关键词格式
	likeKeyword := "%" + keyword + "%"

	// 先计算满足条件的总数据量
	// 加入对状态的查询条件
	db := DB.Model(&Token{}).Where("user_id = ?", userId).Where("name LIKE ?", likeKeyword)
	if status != nil {
		db = db.Where("status = ?", *status)
	}
	err = db.Count(&total).Error
	if err != nil {
		return nil, 0, err
	}

	// 计算分页的偏移量
	offset := (page - 1) * pageSize

	// 获取满足条件的数据的子集
	// 同样加入对状态的查询条件
	db = DB.Where("user_id = ?", userId).Where("name LIKE ?", likeKeyword).Order("id DESC").Offset(offset).Limit(pageSize)
	if status != nil {
		db = db.Where("status = ?", *status)
	}
	err = db.Find(&tokens).Error
	return tokens, total, err
}

func SearchUserTokens(userId int, keyword string) (tokens []*Token, err error) {
	err = DB.Where("user_id = ?", userId).Where("name LIKE ?", keyword+"%").Find(&tokens).Error
	return tokens, err
}

func ValidateUserToken(key string) (token *Token, err error) {
	if key == "" {
		return nil, errors.New("Token not provided")
	}
	token, err = CacheGetTokenByKey(key)
	if err != nil {
		logger.SysError("CacheGetTokenByKey failed: " + err.Error())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("Token not provided")
		}
		return nil, errors.New("Token verification failed")
	}
	if token.Status == common.TokenStatusExhausted {
		return nil, errors.New("The token quota has been exhausted")
	} else if token.Status == common.TokenStatusExpired {
		return nil, errors.New("The token has expired")
	}
	if token.Status != common.TokenStatusEnabled {
		return nil, errors.New("The token status is not available")
	}
	if token.ExpiredTime != -1 && token.ExpiredTime < helper.GetTimestamp() {
		if !common.RedisEnabled {
			token.Status = common.TokenStatusExpired
			err := token.SelectUpdate()
			if err != nil {
				logger.SysError("failed to update token status" + err.Error())
			}
		}
		return nil, errors.New("The token has expired")
	}
	if !token.UnlimitedQuota && token.RemainQuota <= 0 {
		if !common.RedisEnabled {
			// in this case, we can make sure the token is exhausted
			token.Status = common.TokenStatusExhausted
			err := token.SelectUpdate()
			if err != nil {
				logger.SysError("failed to update token status" + err.Error())
			}
		}
		return nil, errors.New("The token quota has been exhausted")
	}
	return token, nil
}

func GetTokenByIds(id int, userId int) (*Token, error) {
	if id == 0 || userId == 0 {
		return nil, errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	var err error = nil
	err = DB.First(&token, "id = ? and user_id = ?", id, userId).Error
	return &token, err
}

func GetTokenById(id int) (*Token, error) {
	if id == 0 {
		return nil, errors.New("id 为空！")
	}
	token := Token{Id: id}
	var err error = nil
	err = DB.First(&token, "id = ?", id).Error
	return &token, err
}

func (token *Token) Insert() error {
	var err error
	err = DB.Create(token).Error
	return err
}

// Update Make sure your token's fields is completed, because this will update non-zero values
func (token *Token) Update() error {
	var err error
	err = DB.Model(token).Select("name", "status", "expired_time", "remain_quota", "token_remind_threshold", "unlimited_quota").Updates(token).Error
	return err
}

func (token *Token) SelectUpdate() error {
	// This can update zero values
	return DB.Model(token).Select("accessed_time", "status").Updates(token).Error
}

func (token *Token) Delete() error {
	var err error
	err = DB.Delete(token).Error
	return err
}

func DeleteTokensByIds(ids []int, userId int) error {
	// 检查ids和userId是否有效
	if len(ids) == 0 || userId == 0 {
		return errors.New("ids列表为空或userId无效")
	}

	// 构造查询条件，只删除属于userId的且ID在ids列表中的token
	// 这里使用了GORM的Delete方法进行批量删除
	result := DB.Where("id IN ? AND user_id = ?", ids, userId).Delete(&Token{})
	if result.Error != nil {
		return result.Error
	}
	return nil
}

func DeleteTokenById(id int, userId int) (err error) {
	// Why we need userId here? In case user want to delete other's token.
	if id == 0 || userId == 0 {
		return errors.New("id 或 userId 为空！")
	}
	token := Token{Id: id, UserId: userId}
	err = DB.Where(token).First(&token).Error
	if err != nil {
		return err
	}
	return token.Delete()
}

func IncreaseTokenQuota(id int, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if config.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, quota)
		return nil
	}
	return increaseTokenQuota(id, quota)
}

func increaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota + ?", quota),
			"used_quota":    gorm.Expr("used_quota - ?", quota),
			"accessed_time": helper.GetTimestamp(),
		},
	).Error
	return err
}

func DecreaseTokenQuota(id int, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if config.BatchUpdateEnabled {
		addNewRecord(BatchUpdateTypeTokenQuota, id, -quota)
		return nil
	}
	return decreaseTokenQuota(id, quota)
}

func decreaseTokenQuota(id int, quota int) (err error) {
	err = DB.Model(&Token{}).Where("id = ?", id).Updates(
		map[string]interface{}{
			"remain_quota":  gorm.Expr("remain_quota - ?", quota),
			"used_quota":    gorm.Expr("used_quota + ?", quota),
			"accessed_time": helper.GetTimestamp(),
		},
	).Error
	return err
}

func PreConsumeTokenQuota(tokenId int, quota int) (err error) {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	token, err := GetTokenById(tokenId)
	if err != nil {
		return err
	}
	if !token.UnlimitedQuota && token.RemainQuota-quota < token.TokenRemindThreshold {
		go func() {
			email, err := GetUserEmail(token.UserId)
			if err != nil {
				logger.SysError("failed to fetch user email: " + err.Error())
			}
			prompt := "您的令牌额度即将用尽"
			if email != "" {
				err = common.SendEmail(prompt, email,
					fmt.Sprintf("%s，当前令牌 %s剩余额度为 %d,已经达到您设定的阈值%d", prompt, token.Name, token.RemainQuota, token.TokenRemindThreshold))
				if err != nil {
					logger.SysError("failed to send email" + err.Error())
				}
			}
		}()
	}
	if !token.UnlimitedQuota && token.RemainQuota < quota {
		return errors.New("令牌额度不足")
	}
	user, err := GetUserById(token.UserId, false)
	userQuota, err := GetUserQuota(token.UserId)
	if err != nil {
		return err
	}
	if userQuota < quota {
		return errors.New("用户额度不足")
	}
	quotaTooLow := userQuota >= user.UserRemindThreshold && userQuota-quota < user.UserRemindThreshold
	noMoreQuota := userQuota-quota <= 0
	if quotaTooLow || noMoreQuota {
		go func() {
			email, err := GetUserEmail(token.UserId)
			if err != nil {
				logger.SysError("failed to fetch user email: " + err.Error())
			}
			prompt := "您的额度即将用尽"
			if noMoreQuota {
				prompt = "您的额度已用尽"
			}
			if email != "" {
				err = common.SendEmail(prompt, email,
					fmt.Sprintf("%s，当前剩余额度为 %d，已经达到您设定的阈值%d", prompt, userQuota, user.UserRemindThreshold))
				if err != nil {
					logger.SysError("failed to send email" + err.Error())
				}
			}
		}()
	}
	if !token.UnlimitedQuota {
		err = DecreaseTokenQuota(tokenId, quota)
		if err != nil {
			return err
		}
	}
	err = DecreaseUserQuota(token.UserId, quota)
	return err
}

func PostConsumeTokenQuota(tokenId int, quota int) (err error) {
	token, err := GetTokenById(tokenId)
	if quota > 0 {
		err = DecreaseUserQuota(token.UserId, quota)
	} else {
		err = IncreaseUserQuota(token.UserId, -quota)
	}
	if err != nil {
		return err
	}
	if !token.UnlimitedQuota {
		if quota > 0 {
			err = DecreaseTokenQuota(tokenId, quota)
		} else {
			err = IncreaseTokenQuota(tokenId, -quota)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
