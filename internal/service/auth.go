package service

import (
	"wxcode/internal/model"
	"wxcode/internal/passwd"
	"wxcode/internal/store"
)

// HashPassword 把明文密码转成可直接落库的哈希串。
//
// 调用方（注册 / 改密 / 管理员重置）先算哈希，再把哈希交给 store 写入；
// 明文只在这个函数的作用域里存在，不落库、也不进 model.User。
func HashPassword(password string) (string, error) { return passwd.Hash(password) }

// CheckPassword 校验密码。
//
// 只比对哈希；哈希参数落后（迭代次数调高过）时，校验成功后顺手重算并写回，
// 用户无感知地完成升级。
func CheckPassword(u *model.User, password string) bool {
	if u == nil || u.PasswordHash == "" {
		return false
	}
	if !passwd.Verify(u.PasswordHash, password) {
		return false
	}
	if passwd.NeedsRehash(u.PasswordHash) {
		if hash, err := passwd.Hash(password); err == nil {
			if err := store.SetPassword(u.ID, hash); err == nil {
				u.PasswordHash = hash
			}
		}
	}
	return true
}
