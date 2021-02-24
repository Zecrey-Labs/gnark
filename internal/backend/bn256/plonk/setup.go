// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package plonk

import (
	"github.com/consensys/gnark/crypto/polynomial"
	"github.com/consensys/gnark/crypto/polynomial/bn256"
	"github.com/consensys/gnark/internal/backend/bn256/cs"
	"github.com/consensys/gnark/internal/backend/bn256/fft"
	"github.com/consensys/gurvy/bn256/fr"
)

// PublicRaw represents the raw public data corresponding to a circuit,
// which consists of the evaluations of the LDE of qr,ql,qm,qo,k. The compact
// version of public data consists of commitments of qr,ql,qm,qo,k.
type PublicRaw struct {

	// Commitment scheme that is used for an instantiation of PLONK
	CommitmentScheme polynomial.CommitmentScheme

	// FFTinv of the LDE of qr,ql,qm,qo,k (so all polynomials are in canonical basis)
	Ql, Qr, Qm, Qo, Qk bn256.Poly

	// Domains used for the FFTs
	DomainNum, DomainH *fft.Domain

	// position -> permuted position (position in [0,3*sizeSystem-1])
	Permutation []int
}

// buildPermutation builds the Permutation associated with a circuit.
//
// The permutation s is composed of cycles of maximum length such that
//
// 			s. (l||r||o) = (l||r||o)
//
//, where l||r||o is the concatenation of the indices of l, r, o in
// ql.l+qr.r+qm.l.r+qo.O+k = 0.
//
// The permutation is encoded as a slice s of size 3*size(l), where the
// i-th entry of l||r||o is sent to the s[i]-th entry, so it acts on a tab
// like this: for i in tab: tab[i] = tab[permutation[i]]
func buildPermutation(spr *cs.SparseR1CS, publicData *PublicRaw) {

	sizeSolution := int(publicData.DomainNum.Cardinality)

	// position -> variable_ID
	lro := make([]int, 3*sizeSolution)

	publicData.Permutation = make([]int, 3*sizeSolution)
	for i := 0; i < len(spr.Constraints); i++ {

		lro[i] = spr.Constraints[i].L.VariableID()
		lro[sizeSolution+i] = spr.Constraints[i].R.VariableID()
		lro[2*sizeSolution+i] = spr.Constraints[i].O.VariableID()

		publicData.Permutation[i] = -1
		publicData.Permutation[sizeSolution+i] = -1
		publicData.Permutation[2*sizeSolution+i] = -1
	}
	offset := len(spr.Constraints)
	for i := 0; i < len(spr.Assertions); i++ {

		lro[offset+i] = spr.Assertions[i].L.VariableID()
		lro[offset+sizeSolution+i] = spr.Assertions[i].R.VariableID()
		lro[offset+2*sizeSolution+i] = spr.Assertions[i].O.VariableID()

		publicData.Permutation[offset+i] = -1
		publicData.Permutation[offset+sizeSolution+i] = -1
		publicData.Permutation[offset+2*sizeSolution+i] = -1
	}
	offset += len(spr.Assertions)
	for i := 0; i < sizeSolution-offset; i++ {

		publicData.Permutation[offset+i] = -1
		publicData.Permutation[offset+sizeSolution+i] = -1
		publicData.Permutation[offset+2*sizeSolution+i] = -1
	}

	nbVariables := spr.NbInternalVariables + spr.NbPublicVariables + spr.NbSecretVariables

	// map ID -> last position the ID was seen
	cycle := make([]int, nbVariables)
	for i := 0; i < len(cycle); i++ {
		cycle[i] = -1
	}

	for i := 0; i < 3*sizeSolution; i++ {
		if cycle[lro[i]] != -1 {
			publicData.Permutation[i] = cycle[lro[i]]
		}
		cycle[lro[i]] = i
	}

	// complete the Permutation by filling the first IDs encountered
	counter := nbVariables
	for iter := 0; counter > 0; iter++ {
		if publicData.Permutation[iter] == -1 {
			publicData.Permutation[iter] = cycle[lro[iter]]
			counter--
		}
	}

}

// Setup from a sparseR1CS, it returns ql, qr, qm, qo, k in
// the canonical basis.
func Setup(spr *cs.SparseR1CS, polynomialCommitment polynomial.CommitmentScheme) *PublicRaw {

	nbConstraints := len(spr.Constraints)
	nbAssertions := len(spr.Assertions)

	var res PublicRaw

	// fft domains
	sizeSystem := uint64(nbConstraints + nbAssertions)
	res.DomainNum = fft.NewDomain(sizeSystem, 2)
	res.DomainH = fft.NewDomain(2*sizeSystem, 1)

	// commitment scheme
	res.CommitmentScheme = polynomialCommitment

	// public polynomials
	res.Ql = make([]fr.Element, res.DomainNum.Cardinality)
	res.Qr = make([]fr.Element, res.DomainNum.Cardinality)
	res.Qm = make([]fr.Element, res.DomainNum.Cardinality)
	res.Qo = make([]fr.Element, res.DomainNum.Cardinality)
	res.Qk = make([]fr.Element, res.DomainNum.Cardinality)
	for i := 0; i < nbConstraints; i++ {

		res.Ql[i].Set(&spr.Coefficients[spr.Constraints[i].L.CoeffID()])
		res.Qr[i].Set(&spr.Coefficients[spr.Constraints[i].R.CoeffID()])
		res.Qm[i].Set(&spr.Coefficients[spr.Constraints[i].M[0].CoeffID()]).
			Mul(&res.Qm[i], &spr.Coefficients[spr.Constraints[i].M[1].CoeffID()])
		res.Qo[i].Set(&spr.Coefficients[spr.Constraints[i].O.CoeffID()])
		res.Qk[i].Set(&spr.Coefficients[spr.Constraints[i].K])
	}
	for i := 0; i < nbAssertions; i++ {

		index := nbConstraints + i

		res.Ql[index].Set(&spr.Coefficients[spr.Assertions[i].L.CoeffID()])
		res.Qr[index].Set(&spr.Coefficients[spr.Assertions[i].R.CoeffID()])
		res.Qm[index].Set(&spr.Coefficients[spr.Assertions[i].M[0].CoeffID()]).
			Mul(&res.Qm[index], &spr.Coefficients[spr.Assertions[i].M[1].CoeffID()])
		res.Qo[index].Set(&spr.Coefficients[spr.Assertions[i].O.CoeffID()])
		res.Qk[index].Set(&spr.Coefficients[spr.Assertions[i].K])
	}

	res.DomainNum.FFTInverse(res.Ql, fft.DIF, 0)
	res.DomainNum.FFTInverse(res.Qr, fft.DIF, 0)
	res.DomainNum.FFTInverse(res.Qm, fft.DIF, 0)
	res.DomainNum.FFTInverse(res.Qo, fft.DIF, 0)
	res.DomainNum.FFTInverse(res.Qk, fft.DIF, 0)
	fft.BitReverse(res.Ql)
	fft.BitReverse(res.Qr)
	fft.BitReverse(res.Qm)
	fft.BitReverse(res.Qo)
	fft.BitReverse(res.Qk)

	// build permutation
	buildPermutation(spr, &res)

	return &res
}
